package mistral

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/sse"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type languageModel struct {
	provider *Provider
	modelID  string
}

func (m *languageModel) ModelID() string      { return m.modelID }
func (m *languageModel) ProviderName() string { return providerName }
func (m *languageModel) Capabilities() provider.Capabilities {
	return provider.Capabilities{NativeJSON: true}
}

// mistralAuthHeader is the HTTP header carrying the API key; extra headers
// from provider.Call.Headers must not be able to override it.
const mistralAuthHeader = "Authorization"

func (m *languageModel) doRequest(ctx context.Context, req chatRequest, providerOptions map[string]any, headers map[string]string) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("mistral: marshal request: %w", err)
	}
	body, err = applyProviderOptions(body, providerOptions)
	if err != nil {
		return nil, fmt.Errorf("mistral: apply provider options: %w", err)
	}

	url := m.provider.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mistral: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(mistralAuthHeader, "Bearer "+m.provider.apiKey)
	for k, v := range headers {
		if strings.EqualFold(k, mistralAuthHeader) {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	return m.provider.client().Do(httpReq)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	req, err := buildChatRequest(m.modelID, call, false)
	if err != nil {
		return nil, err
	}

	resp, err := m.doRequest(ctx, req, call.ProviderOptions, call.Headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mistral: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr chatResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("mistral: decode response: %w", err)
	}

	return convertResponse(wr, body), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildChatRequest(m.modelID, call, true)
	if err != nil {
		return nil, err
	}

	resp, err := m.doRequest(ctx, req, call.ProviderOptions, call.Headers)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("mistral: read error response: %w", readErr)
		}
		return nil, apiError(resp, body)
	}

	return &streamResponse{body: resp.Body}, nil
}

// ---- Streaming ----

type toolCallAccumulator struct {
	id   string
	name string
	args strings.Builder
}

type streamResponse struct {
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

		toolAcc := map[int]*toolCallAccumulator{}
		var toolOrder []int
		var finishReason provider.FinishReason
		haveFinish := false
		var usage provider.Usage

		for ev, err := range sse.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("mistral: stream read: %w", err)
				return
			}

			data := strings.TrimSpace(ev.Data)
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				reason := finishReason
				if !haveFinish {
					reason = provider.FinishOther
				}
				if !yield(provider.FinishPart{Reason: reason, Usage: usage}) {
					return
				}
				return
			}

			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				s.err = fmt.Errorf("mistral: decode stream chunk: %w", err)
				return
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				if choice.Delta.Content != "" {
					if !yield(provider.TextDelta{Text: choice.Delta.Content}) {
						return
					}
				}

				for _, d := range choice.Delta.ToolCalls {
					acc, ok := toolAcc[d.Index]
					if !ok {
						acc = &toolCallAccumulator{}
						toolAcc[d.Index] = acc
						toolOrder = append(toolOrder, d.Index)
					}
					if d.ID != "" {
						acc.id = d.ID
					}
					if d.Function.Name != "" {
						acc.name = d.Function.Name
					}
					acc.args.WriteString(d.Function.Arguments)

					if !yield(provider.ToolCallDelta{
						ID:        acc.id,
						Name:      acc.name,
						ArgsDelta: d.Function.Arguments,
					}) {
						return
					}
				}

				if choice.FinishReason != nil {
					finishReason = mapFinishReason(*choice.FinishReason)
					haveFinish = true

					for _, idx := range toolOrder {
						acc := toolAcc[idx]
						args := acc.args.String()
						if args == "" {
							args = "{}"
						}
						end := provider.ToolCallEnd{Call: provider.ToolCallPart{
							ID:   acc.id,
							Name: acc.name,
							Args: json.RawMessage(args),
						}}
						if !yield(end) {
							return
						}
					}
				}
			}

			// Usage arrives on the final content chunk (usage field
			// non-null), same as chat-completions-style providers.
			if chunk.Usage != nil {
				usage = provider.Usage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
					TotalTokens:  chunk.Usage.TotalTokens,
				}
			}
		}

		// The SSE stream ended (server closed the connection) without a
		// "data: [DONE]" event. Rule: if a finish_reason chunk WAS seen,
		// treat this as a well-formed-enough stream and emit the single
		// FinishPart with whatever usage is known (possibly zero) — some
		// proxies/load balancers drop the trailing [DONE] sentinel after
		// forwarding the real finish_reason chunk. If no finish_reason was
		// ever received, the stream was truncated mid-response: no
		// FinishPart is emitted and Err() reports it instead, per the
		// "exactly ONE FinishPart" contract (zero is preferable to a
		// fabricated one here).
		if haveFinish {
			if !yield(provider.FinishPart{Reason: finishReason, Usage: usage}) {
				return
			}
			return
		}
		s.err = errors.New("mistral: stream ended unexpectedly without [DONE]")
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
