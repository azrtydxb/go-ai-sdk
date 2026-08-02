package cohere

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

func (m *languageModel) doRequest(ctx context.Context, req chatRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cohere: marshal request: %w", err)
	}

	url := m.provider.baseURL + "/chat"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("cohere: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

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

	resp, err := m.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cohere: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr chatResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("cohere: decode response: %w", err)
	}

	return convertResponse(wr, body), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildChatRequest(m.modelID, call, true)
	if err != nil {
		return nil, err
	}

	resp, err := m.doRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("cohere: read error response: %w", readErr)
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

		for ev, err := range sse.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("cohere: stream read: %w", err)
				return
			}

			data := strings.TrimSpace(ev.Data)
			if data == "" {
				continue
			}

			var se streamEvent
			if err := json.Unmarshal([]byte(data), &se); err != nil {
				s.err = fmt.Errorf("cohere: decode stream event: %w", err)
				return
			}

			idx := 0
			if se.Index != nil {
				idx = *se.Index
			}

			switch se.Type {
			case "message-start", "content-start", "content-end", "tool-plan-delta":
				// No StreamPart carries this information; ignored per the
				// wire mapping.
				continue

			case "content-delta":
				if se.Delta == nil || se.Delta.Message == nil || se.Delta.Message.Content == nil {
					continue
				}
				text := se.Delta.Message.Content.Text
				if text == "" {
					continue
				}
				if !yield(provider.TextDelta{Text: text}) {
					return
				}

			case "tool-call-start":
				acc := &toolCallAccumulator{}
				toolAcc[idx] = acc
				if se.Delta != nil && se.Delta.Message != nil && se.Delta.Message.ToolCalls != nil {
					tc := se.Delta.Message.ToolCalls
					acc.id = tc.ID
					if tc.Function != nil {
						acc.name = tc.Function.Name
					}
				}
				if !yield(provider.ToolCallDelta{ID: acc.id, Name: acc.name}) {
					return
				}

			case "tool-call-delta":
				acc, ok := toolAcc[idx]
				if !ok {
					acc = &toolCallAccumulator{}
					toolAcc[idx] = acc
				}
				var argsDelta string
				if se.Delta != nil && se.Delta.Message != nil && se.Delta.Message.ToolCalls != nil && se.Delta.Message.ToolCalls.Function != nil {
					argsDelta = se.Delta.Message.ToolCalls.Function.Arguments
				}
				acc.args.WriteString(argsDelta)
				if !yield(provider.ToolCallDelta{ID: acc.id, Name: acc.name, ArgsDelta: argsDelta}) {
					return
				}

			case "tool-call-end":
				acc, ok := toolAcc[idx]
				if !ok {
					continue
				}
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

			case "message-end":
				var reason provider.FinishReason
				var usage provider.Usage
				if se.Delta != nil {
					reason = mapFinishReason(se.Delta.FinishReason)
					if se.Delta.Usage != nil {
						usage = provider.Usage{
							InputTokens:  int(se.Delta.Usage.Tokens.InputTokens),
							OutputTokens: int(se.Delta.Usage.Tokens.OutputTokens),
							TotalTokens:  int(se.Delta.Usage.Tokens.InputTokens) + int(se.Delta.Usage.Tokens.OutputTokens),
						}
					}
				} else {
					reason = provider.FinishOther
				}
				// message-end is the ONLY event carrying finish_reason;
				// emit the single FinishPart here and stop. This is the
				// exclusive source of "was the stream well-formed" — see
				// the truncation rule below.
				if !yield(provider.FinishPart{Reason: reason, Usage: usage}) {
					return
				}
				return

			default:
				// Unknown/future event type: ignore rather than fail.
				continue
			}
		}

		// Truncation rule: message-end is the only event that carries
		// finish_reason and triggers the single FinishPart (handled above,
		// which returns immediately). If control reaches here, the SSE
		// stream ended (EOF or connection close) without ever emitting a
		// message-end event, so finish_reason was never seen. Per the
		// "exactly ONE FinishPart" contract, emit zero FinishParts and
		// report the truncation via Err() instead of fabricating one.
		s.err = errors.New("cohere: stream ended unexpectedly without message-end")
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
