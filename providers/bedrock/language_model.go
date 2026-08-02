package bedrock

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
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/eventstream"
	"github.com/azrtydxb/go-ai-sdk/internal/sigv4"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type languageModel struct {
	provider *Provider
	modelID  string
}

func (m *languageModel) ModelID() string      { return m.modelID }
func (m *languageModel) ProviderName() string { return providerName }
func (m *languageModel) Capabilities() provider.Capabilities {
	// Bedrock's Converse API has no schema-constrained JSON response mode;
	// the ai core falls back to tool-mode object generation.
	return provider.Capabilities{NativeJSON: false}
}

func (m *languageModel) modelPath(suffix string) string {
	return m.provider.modelPath(m.modelID, suffix)
}

// modelPath builds the "/model/{escaped id}{suffix}" path shared by the
// language model (/converse, /converse-stream) and the embedding model
// (/invoke).
func (p *Provider) modelPath(modelID, suffix string) string {
	return p.baseURL + "/model/" + escapeModelID(modelID) + suffix
}

// escapeModelID percent-encodes a Bedrock model ID for use as a single URL
// path segment. Bedrock model IDs commonly contain ':' (e.g.
// "anthropic.claude-3-sonnet-20240229-v1:0"), which url.PathEscape leaves
// unescaped since ':' is technically legal in a path segment — but Bedrock
// (like most AWS service APIs) expects it percent-encoded, and the SigV4
// canonical request must be computed against the same bytes actually sent
// on the wire. This escapes everything outside SigV4's unreserved set
// (A-Z a-z 0-9 - _ . ~).
func escapeModelID(id string) string {
	var buf strings.Builder
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			buf.WriteByte(c)
		} else {
			fmt.Fprintf(&buf, "%%%02X", c)
		}
	}
	return buf.String()
}

func (m *languageModel) doRequest(ctx context.Context, path string, body []byte) (*http.Response, error) {
	return m.provider.doRequest(ctx, path, body)
}

// doRequest builds a SigV4-signed POST request against path with the given
// body and executes it. Shared by the language model (converse /
// converse-stream) and the embedding model (Titan /invoke).
func (p *Provider) doRequest(ctx context.Context, path string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if err := sigv4.Sign(httpReq, body, p.creds, p.region, defaultServiceOpt, time.Now()); err != nil {
		return nil, fmt.Errorf("bedrock: sign request: %w", err)
	}

	resp, err := p.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	req, err := buildConverseRequest(call)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal request: %w", err)
	}

	resp, err := m.doRequest(ctx, m.modelPath("/converse"), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bedrock: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, respBody)
	}

	var wr converseResponse
	if err := json.Unmarshal(respBody, &wr); err != nil {
		return nil, fmt.Errorf("bedrock: decode response: %w", err)
	}

	return convertResponse(wr, respBody), nil
}

func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	req, err := buildConverseRequest(call)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal request: %w", err)
	}

	resp, err := m.doRequest(ctx, m.modelPath("/converse-stream"), body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("bedrock: read error response: %w", readErr)
		}
		return nil, apiError(resp, respBody)
	}

	return &streamResponse{body: resp.Body}, nil
}

// ---- Streaming ----

type blockAccumulator struct {
	isTool bool
	id     string
	name   string
	args   strings.Builder
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

		blocks := map[int]*blockAccumulator{}
		var finishReason provider.FinishReason
		haveFinish := false
		var usage provider.Usage

		for msg, err := range eventstream.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("bedrock: stream read: %w", err)
				return
			}

			if msg.Headers[":message-type"] == "exception" {
				var exc eventException
				_ = json.Unmarshal(msg.Payload, &exc)
				excType := msg.Headers[":exception-type"]
				if exc.Message != "" {
					s.err = fmt.Errorf("bedrock: stream exception %s: %s", excType, exc.Message)
				} else {
					s.err = fmt.Errorf("bedrock: stream exception %s", excType)
				}
				return
			}

			// A ":message-type" of "error" is a transport-level event
			// stream error (as opposed to "exception", a modeled
			// application-level error): the connection is being
			// terminated by the server, and the error details are carried
			// in the ":error-code" / ":error-message" headers rather than
			// a JSON payload.
			if msg.Headers[":message-type"] == "error" {
				code := msg.Headers[":error-code"]
				message := msg.Headers[":error-message"]
				switch {
				case code != "" && message != "":
					s.err = fmt.Errorf("bedrock: stream transport error %s: %s", code, message)
				case code != "":
					s.err = fmt.Errorf("bedrock: stream transport error %s", code)
				case message != "":
					s.err = fmt.Errorf("bedrock: stream transport error: %s", message)
				default:
					s.err = errors.New("bedrock: stream transport error")
				}
				return
			}

			switch msg.Headers[":event-type"] {
			case "messageStart":
				// No StreamPart emitted; role is always "assistant".

			case "contentBlockStart":
				var ev eventContentBlockStart
				if err := json.Unmarshal(msg.Payload, &ev); err != nil {
					s.err = fmt.Errorf("bedrock: decode contentBlockStart: %w", err)
					return
				}
				if ev.Start.ToolUse != nil {
					blocks[ev.ContentBlockIndex] = &blockAccumulator{
						isTool: true,
						id:     ev.Start.ToolUse.ToolUseID,
						name:   ev.Start.ToolUse.Name,
					}
				}

			case "contentBlockDelta":
				var ev eventContentBlockDelta
				if err := json.Unmarshal(msg.Payload, &ev); err != nil {
					s.err = fmt.Errorf("bedrock: decode contentBlockDelta: %w", err)
					return
				}
				if ev.Delta.Text != "" {
					if !yield(provider.TextDelta{Text: ev.Delta.Text}) {
						return
					}
				}
				if ev.Delta.ToolUse != nil {
					acc, ok := blocks[ev.ContentBlockIndex]
					if !ok {
						acc = &blockAccumulator{isTool: true}
						blocks[ev.ContentBlockIndex] = acc
					}
					acc.args.WriteString(ev.Delta.ToolUse.Input)
					if !yield(provider.ToolCallDelta{
						ID:        acc.id,
						Name:      acc.name,
						ArgsDelta: ev.Delta.ToolUse.Input,
					}) {
						return
					}
				}

			case "contentBlockStop":
				var ev eventContentBlockStop
				if err := json.Unmarshal(msg.Payload, &ev); err != nil {
					s.err = fmt.Errorf("bedrock: decode contentBlockStop: %w", err)
					return
				}
				acc, ok := blocks[ev.ContentBlockIndex]
				if ok && acc.isTool {
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

			case "messageStop":
				var ev eventMessageStop
				if err := json.Unmarshal(msg.Payload, &ev); err != nil {
					s.err = fmt.Errorf("bedrock: decode messageStop: %w", err)
					return
				}
				finishReason = mapStopReason(ev.StopReason)
				haveFinish = true

			case "metadata":
				var ev eventMetadata
				if err := json.Unmarshal(msg.Payload, &ev); err != nil {
					s.err = fmt.Errorf("bedrock: decode metadata: %w", err)
					return
				}
				usage = provider.Usage{
					InputTokens:  ev.Usage.InputTokens,
					OutputTokens: ev.Usage.OutputTokens,
					TotalTokens:  ev.Usage.TotalTokens,
				}
			}
		}

		// Truncation rule: a stopReason (messageStop event) was seen, so
		// this is a well-formed stream end — emit the single required
		// FinishPart. If messageStop was never seen, the stream was cut
		// short (dropped connection, proxy timeout, ...): no FinishPart is
		// emitted and Err() reports the truncation instead, per the
		// "exactly ONE FinishPart" contract.
		if haveFinish {
			if !yield(provider.FinishPart{Reason: finishReason, Usage: usage}) {
				return
			}
			return
		}
		s.err = errors.New("bedrock: stream ended unexpectedly without messageStop")
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
