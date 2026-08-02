package anthropic

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
func (m *languageModel) ProviderName() string { return "anthropic" }
func (m *languageModel) Capabilities() provider.Capabilities {
	return provider.Capabilities{NativeJSON: false}
}

func (m *languageModel) doRequest(ctx context.Context, req messagesRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	url := m.provider.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", m.provider.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	return m.provider.client().Do(httpReq)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	req, err := buildMessagesRequest(m.modelID, call, false)
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
		return nil, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr messageResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return convertResponse(wr, body), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildMessagesRequest(m.modelID, call, true)
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
			return nil, fmt.Errorf("anthropic: read error response: %w", readErr)
		}
		return nil, apiError(resp, body)
	}

	return &streamResponse{body: resp.Body}, nil
}

// ---- Streaming ----

type toolBlockAccumulator struct {
	id      string
	name    string
	isTool  bool
	partial strings.Builder
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

		blocks := map[int]*toolBlockAccumulator{}
		var usage provider.Usage
		var stopReason string
		haveStopReason := false
		sawMessageStop := false

		for ev, err := range sse.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("anthropic: stream read: %w", err)
				return
			}

			data := strings.TrimSpace(ev.Data)
			if data == "" {
				continue
			}

			switch ev.Event {
			case "message_start":
				var e messageStartEvent
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					s.err = fmt.Errorf("anthropic: decode message_start: %w", err)
					return
				}
				if e.Message.Usage != nil {
					usage.InputTokens = e.Message.Usage.InputTokens
					usage.TotalTokens = usage.InputTokens + usage.OutputTokens
				}

			case "content_block_start":
				var e contentBlockStartEvent
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					s.err = fmt.Errorf("anthropic: decode content_block_start: %w", err)
					return
				}
				if e.ContentBlock.Type == "tool_use" {
					blocks[e.Index] = &toolBlockAccumulator{
						id:     e.ContentBlock.ID,
						name:   e.ContentBlock.Name,
						isTool: true,
					}
				}

			case "content_block_delta":
				var e contentBlockDeltaEvent
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					s.err = fmt.Errorf("anthropic: decode content_block_delta: %w", err)
					return
				}
				switch e.Delta.Type {
				case "text_delta":
					if !yield(provider.TextDelta{Text: e.Delta.Text}) {
						return
					}
				case "input_json_delta":
					acc := blocks[e.Index]
					var id, name string
					if acc != nil {
						acc.partial.WriteString(e.Delta.PartialJSON)
						id, name = acc.id, acc.name
					}
					if !yield(provider.ToolCallDelta{
						ID:        id,
						Name:      name,
						ArgsDelta: e.Delta.PartialJSON,
					}) {
						return
					}
				}

			case "content_block_stop":
				var e contentBlockStopEvent
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					s.err = fmt.Errorf("anthropic: decode content_block_stop: %w", err)
					return
				}
				if acc, ok := blocks[e.Index]; ok && acc.isTool {
					args := acc.partial.String()
					if args == "" {
						args = "{}"
					}
					if !yield(provider.ToolCallEnd{Call: provider.ToolCallPart{
						ID:   acc.id,
						Name: acc.name,
						Args: json.RawMessage(args),
					}}) {
						return
					}
				}

			case "message_delta":
				var e messageDeltaEvent
				if err := json.Unmarshal([]byte(data), &e); err != nil {
					s.err = fmt.Errorf("anthropic: decode message_delta: %w", err)
					return
				}
				if e.Delta.StopReason != "" {
					stopReason = e.Delta.StopReason
					haveStopReason = true
				}
				if e.Usage != nil {
					usage.OutputTokens = e.Usage.OutputTokens
					usage.TotalTokens = usage.InputTokens + usage.OutputTokens
				}

			case "message_stop":
				sawMessageStop = true
				reason := provider.FinishOther
				if haveStopReason {
					reason = mapStopReason(stopReason)
				}
				if !yield(provider.FinishPart{Reason: reason, Usage: usage}) {
					return
				}
				return

			case "error":
				var we wireError
				json.Unmarshal([]byte(data), &we)
				s.err = fmt.Errorf("anthropic: stream error: %s", we.Error.Message)
				return

			default:
				// ping and any other/unrecognized named events: ignore.
			}
		}

		if sawMessageStop {
			return
		}

		// The SSE stream ended (server closed the connection) without a
		// "message_stop" event. Rule (mirrors the OpenAI provider's
		// [DONE]-robustness fix): if a stop_reason WAS observed via a
		// message_delta event, treat the stream as well-formed enough and
		// emit the single FinishPart with whatever usage is known — some
		// proxies drop the trailing message_stop after forwarding the real
		// stop_reason. If no stop_reason was ever received, the stream was
		// truncated mid-response: no FinishPart is emitted and Err()
		// reports it instead, per the "exactly ONE FinishPart" contract.
		if haveStopReason {
			if !yield(provider.FinishPart{Reason: mapStopReason(stopReason), Usage: usage}) {
				return
			}
			return
		}
		s.err = errors.New("anthropic: stream ended unexpectedly without message_stop")
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
