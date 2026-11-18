package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"slices"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/internal/sse"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewLanguageModel returns a provider.LanguageModel that speaks the
// OpenAI chat-completions wire format against cfg.
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
	return provider.Capabilities{NativeJSON: m.cfg.NativeJSON}
}

func (m *languageModel) doRequest(ctx context.Context, req chatRequest, providerOptions map[string]any, headers map[string]string) (*http.Response, error) {
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal request: %w", err)
	}
	body, err = providerutil.ApplyProviderOptions(body, providerOptions, m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: apply provider options: %w", err)
	}

	url := m.cfg.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	m.cfg.setAuthHeader(httpReq)
	httpheader.Apply(httpReq, headers, m.cfg.authHeaderName())

	return m.cfg.client().Do(httpReq)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallErrorFromResponse(resp, string(body), providerutil.ErrorMessage(body))
}

func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	req, err := buildChatRequest(m.cfg, m.modelID, call, false)
	if err != nil {
		return nil, err
	}

	resp, err := m.doRequest(ctx, req, call.ProviderOptions, call.Headers)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr chatResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode response: %w", err)
	}

	return convertResponse(wr, body, m.cfg.Name), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildChatRequest(m.cfg, m.modelID, call, true)
	if err != nil {
		return nil, err
	}

	resp, err := m.doRequest(ctx, req, call.ProviderOptions, call.Headers)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("openaicompat: read error response: %w", readErr)
		}
		return nil, apiError(resp, body)
	}

	return &streamResponse{body: resp.Body, providerName: m.cfg.Name, lenient: m.cfg.LenientStream}, nil
}

// ---- Streaming ----

type toolCallAccumulator struct {
	id   string
	name string
	args strings.Builder
}

type streamResponse struct {
	body         io.ReadCloser
	providerName string
	lenient      bool
	err          error
	used         bool
	closed       bool
}

func (s *streamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		if s.used {
			return
		}
		s.used = true

		toolAcc := map[int]*toolCallAccumulator{}
		toolsEnded := false
		// endTools emits ToolCallEnd for every accumulated call, ordered by
		// the deltas' index, once per stream.
		endTools := func() bool {
			if toolsEnded {
				return true
			}
			toolsEnded = true
			idxs := make([]int, 0, len(toolAcc))
			for idx := range toolAcc {
				idxs = append(idxs, idx)
			}
			slices.Sort(idxs)
			for _, idx := range idxs {
				acc := toolAcc[idx]
				args := acc.args.String()
				if args == "" {
					args = "{}"
				}
				if !yield(provider.ToolCallEnd{Call: provider.ToolCallPart{
					ID:   acc.id,
					Name: acc.name,
					Args: json.RawMessage(args),
				}}) {
					return false
				}
			}
			return true
		}
		var finishReason provider.FinishReason
		haveFinish := false
		var usage provider.Usage
		hasUsage := false
		var systemFingerprint string

		scan := sse.Scan
		if s.lenient {
			scan = sse.ScanLenient
		}
		for ev, err := range scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("openaicompat: stream read: %w", err)
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
				if !endTools() {
					return
				}
				yield(provider.FinishPart{Reason: reason, Usage: usage, HasUsage: hasUsage, ProviderMetadata: s.fingerprintMetadata(systemFingerprint)})
				return
			}

			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				if s.lenient {
					continue // keep-alive text, partial proxy line, ...
				}
				s.err = fmt.Errorf("openaicompat: decode stream chunk: %w", err)
				return
			}
			if systemFingerprint == "" && chunk.SystemFingerprint != "" {
				systemFingerprint = chunk.SystemFingerprint
			}

			if len(chunk.Choices) > 0 {
				choice := chunk.Choices[0]

				if text := reasoningText(choice.Delta.ReasoningContent, choice.Delta.Reasoning); text != "" {
					if !yield(provider.ReasoningDelta{Text: text}) {
						return
					}
				}

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

					if !endTools() {
						return
					}
				}
			}

			if chunk.Usage != nil {
				usage = convertUsage(*chunk.Usage)
				hasUsage = true
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
			yield(provider.FinishPart{Reason: finishReason, Usage: usage, HasUsage: hasUsage, ProviderMetadata: s.fingerprintMetadata(systemFingerprint)})
			return
		}
		if s.lenient {
			// Clean EOF with no finish_reason: flush what accumulated and
			// finish with FinishOther rather than losing it to an error.
			if endTools() {
				yield(provider.FinishPart{Reason: provider.FinishOther, Usage: usage, HasUsage: hasUsage, ProviderMetadata: s.fingerprintMetadata(systemFingerprint)})
			}
			return
		}
		s.err = errors.New("openaicompat: stream ended unexpectedly without [DONE]")
	}
}

// fingerprintMetadata returns the ProviderMetadata map for a streamed
// FinishPart when fp (the first non-empty system_fingerprint observed
// across the stream's chunks) is non-empty, or nil otherwise — the
// streaming analogue of convertResponse's non-streaming ProviderMetadata
// population.
func (s *streamResponse) fingerprintMetadata(fp string) map[string]any {
	if fp == "" {
		return nil
	}
	return map[string]any{
		s.providerName: map[string]any{
			"system_fingerprint": fp,
		},
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
