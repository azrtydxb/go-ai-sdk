package geminicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/sse"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewLanguageModel returns a provider.LanguageModel that speaks the Gemini
// generateContent/streamGenerateContent wire format against cfg.
func NewLanguageModel(cfg Config, modelID string) provider.LanguageModel {
	return &languageModel{cfg: cfg, modelID: modelID}
}

type languageModel struct {
	cfg     Config
	modelID string
}

func (m *languageModel) ModelID() string      { return m.modelID }
func (m *languageModel) ProviderName() string { return m.cfg.Name }
func (m *languageModel) Capabilities() provider.Capabilities {
	return provider.Capabilities{NativeJSON: true}
}

func (m *languageModel) doRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", m.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := m.cfg.Authorize(ctx, httpReq); err != nil {
		return nil, fmt.Errorf("%s: authorize request: %w", m.cfg.Name, err)
	}

	return m.cfg.client().Do(httpReq)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	req, err := buildGenerateContentRequest(m.modelID, call)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", m.cfg.Name, err)
	}
	body, err = applyProviderOptions(body, call.ProviderOptions, m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("%s: apply provider options: %w", m.cfg.Name, err)
	}

	url := m.cfg.EndpointFor(m.modelID, "generateContent")
	resp, err := m.doRequest(ctx, url, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read response: %w", m.cfg.Name, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, respBody)
	}

	var wr generateContentResponse
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", m.cfg.Name, err)
	}

	return convertResponse(wr, respBody), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildGenerateContentRequest(m.modelID, call)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", m.cfg.Name, err)
	}
	body, err = applyProviderOptions(body, call.ProviderOptions, m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("%s: apply provider options: %w", m.cfg.Name, err)
	}

	// EndpointFor is called without query parameters; ?alt=sse is appended
	// here so EndpointFor implementations stay query-free.
	url := m.cfg.EndpointFor(m.modelID, "streamGenerateContent") + "?alt=sse"
	resp, err := m.doRequest(ctx, url, body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("%s: read error response: %w", m.cfg.Name, readErr)
		}
		return nil, apiError(resp, respBody)
	}

	return &streamResponse{cfg: m.cfg, body: resp.Body}, nil
}

// ---- Streaming ----

type streamResponse struct {
	cfg    Config
	body   io.ReadCloser
	err    error
	used   bool
	closed bool
}

func (s *streamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		if s.used {
			return
		}
		s.used = true

		var usage provider.Usage
		var finishReason provider.FinishReason
		haveFinish := false
		sawFunctionCall := false
		// toolCallIndex is a monotonically increasing counter used to
		// synthesize tool-call IDs across the WHOLE stream, not per SSE
		// chunk. Gemini's functionCall parts carry no ID of their own, and
		// resetting the counter per chunk (e.g. using the part's index
		// within cand.Content.Parts) would produce colliding IDs whenever
		// two separate calls to the same tool name arrive in different
		// chunks (both indexed 0), breaking ID-based tool-call/result
		// correlation downstream.
		toolCallIndex := 0

		for ev, err := range sse.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("%s: stream read: %w", s.cfg.Name, err)
				return
			}

			data := strings.TrimSpace(ev.Data)
			if data == "" {
				continue
			}

			var chunk generateContentResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				s.err = fmt.Errorf("%s: decode stream chunk: %w", s.cfg.Name, err)
				return
			}

			if chunk.UsageMetadata != nil {
				usage = provider.Usage{
					InputTokens:  chunk.UsageMetadata.PromptTokenCount,
					OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
					TotalTokens:  chunk.UsageMetadata.TotalTokenCount,
				}
			}

			if len(chunk.Candidates) == 0 {
				continue
			}
			cand := chunk.Candidates[0]

			for _, part := range cand.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					sawFunctionCall = true
					args := part.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					call := provider.ToolCallPart{
						ID:   fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, toolCallIndex),
						Name: part.FunctionCall.Name,
						Args: args,
					}
					toolCallIndex++
					if !yield(provider.ToolCallDelta{ID: call.ID, Name: call.Name, ArgsDelta: string(args)}) {
						return
					}
					if !yield(provider.ToolCallEnd{Call: call}) {
						return
					}
				case part.Text != "":
					if !yield(provider.TextDelta{Text: part.Text}) {
						return
					}
				}
			}

			if cand.FinishReason != "" {
				finishReason = mapFinishReason(cand.FinishReason, sawFunctionCall)
				haveFinish = true
			}
		}

		// Gemini has no [DONE]/message_stop sentinel — the stream just ends.
		// Rule (mirrors the openai/anthropic providers): if a finishReason
		// WAS observed in some chunk, treat the stream as well-formed enough
		// and emit the single FinishPart at natural EOF. If no finishReason
		// was ever received, the stream was truncated mid-response: no
		// FinishPart is emitted and Err() reports it instead.
		if haveFinish {
			if !yield(provider.FinishPart{Reason: finishReason, Usage: usage}) {
				return
			}
			return
		}
		s.err = fmt.Errorf("%s: stream ended unexpectedly without a finishReason", s.cfg.Name)
	}
}

func (s *streamResponse) Err() error { return s.err }

func (s *streamResponse) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
}
