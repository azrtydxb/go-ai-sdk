// Package compattest provides a shared httptest fixture server that speaks
// the OpenAI chat-completions + embeddings wire format, for use by
// provider/providertest conformance runs and other tests of
// internal/openaicompat-based providers.
//
// It intentionally does not import internal/openaicompat's unexported wire
// types: it defines its own minimal mirror of the wire shapes it needs to
// produce, which keeps it usable as a true black-box fixture of "whatever
// speaks the OpenAI wire format" rather than being coupled to openaicompat's
// internals.
//
// The generic recording-server scaffolding (recording log, body reading,
// SSE framing, canned error scenarios) lives in internal/testserver; this
// package adds only the OpenAI wire-format handlers and scenario mapping.
package compattest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/testserver"
)

// onePixelPNGBase64 is a 1x1 transparent PNG, base64-encoded — used as the
// canned response for the /images/generations fixture.
const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

// OnePixelPNGBase64 returns the base64-encoded 1x1 PNG the
// /images/generations fixture responds with, so callers can assert on the
// decoded bytes.
func OnePixelPNGBase64() string { return onePixelPNGBase64 }

// ---- wire types (request side, just enough to decode) ----

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type chatRequest struct {
	Messages []wireMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ---- wire types (response side) ----

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatResponseMessage struct {
	Content   *string        `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls,omitempty"`
}

type chatResponseChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatResponse struct {
	Choices []chatResponseChoice `json:"choices"`
	Usage   wireUsage            `json:"usage"`
}

type wireToolCallDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function wireToolCallFunc `json:"function"`
}

type chatStreamDelta struct {
	Content   string              `json:"content,omitempty"`
	ToolCalls []wireToolCallDelta `json:"tool_calls,omitempty"`
}

type chatStreamChoice struct {
	Delta        chatStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason,omitempty"`
}

type chatStreamChunk struct {
	Choices []chatStreamChoice `json:"choices,omitempty"`
	Usage   *wireUsage         `json:"usage,omitempty"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Usage wireUsage       `json:"usage"`
}

// Server is a fixture httptest.Server speaking the OpenAI chat-completions
// and embeddings wire format. It reuses the recording log from
// internal/testserver: Requests() returns every request body in arrival
// order, HeaderValues(name) the matching header values, AuthHeaders() the
// Authorization values. Callers must Close it (or rely on the t.Cleanup
// registered by NewFixtureServer).
type Server = testserver.Server

// lastUserText extracts the text of the last user message's Content field,
// which the fixtures below always send as a plain JSON string.
func lastUserText(req chatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		var s string
		if err := json.Unmarshal(m.Content, &s); err == nil {
			return s
		}
	}
	return ""
}

// writeSSE frames v as a single "data:" SSE event and flushes — a thin
// wrapper over internal/testserver keeping the handler bodies below
// readable.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	testserver.WriteSSE(w, flusher, v)
}

// NewFixtureServer returns an httptest.Server speaking the OpenAI
// chat-completions + embeddings wire format, implementing the six
// providertest scenarios keyed off the LAST user message text, with
// "simple" responding "Hello from <providerName>!". Callers must Close it
// (handled automatically via t.Cleanup).
func NewFixtureServer(t *testing.T, providerName string) *Server {
	t.Helper()

	mux := http.NewServeMux()

	// sPtr is an atomic pointer so mux handlers can safely read s even
	// if httptest.NewServer starts accepting requests before we assign
	// the real value below.
	var sPtr atomic.Pointer[testserver.Server]

	// All handlers read sPtr.Load() at call time, so they always see the
	// latest value, even if testserver.New starts its goroutine before
	// the sPtr.Store call below completes.
	handleChat := func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
		var req chatRequest
		if !testserver.DecodeAndRecord(t, s, w, r, &req) {
			return
		}
		chatImpl(t, w, r, req, providerName)
	}

	mux.HandleFunc("/chat/completions", handleChat)
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
		var req embeddingRequest
		if !testserver.DecodeAndRecord(t, s, w, r, &req) {
			return
		}
		embedImpl(w, req)
	})
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
		raw, ok := testserver.ReadBody(t, w, r)
		if !ok {
			return
		}
		s.Record(raw, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, onePixelPNGBase64)
	})
	mux.HandleFunc("/audio/speech", func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
		raw, ok := testserver.ReadBody(t, w, r)
		if !ok {
			return
		}
		s.Record(raw, r.Header.Clone())
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("FAKEAUDIO"))
	})
	mux.HandleFunc("/audio/transcriptions", func(w http.ResponseWriter, r *http.Request) {
		s := sPtr.Load()
		raw, ok := testserver.ReadBody(t, w, r)
		if !ok {
			return
		}
		s.Record(raw, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"text":"hello world","language":"en","duration":1.5,`+
			`"segments":[{"text":"hello","start":0,"end":0.5},{"text":"world","start":0.5,"end":1.5}]}`)
	})

	s := testserver.New(t, mux)
	sPtr.Store(s)
	return s
}

func chatImpl(t *testing.T, w http.ResponseWriter, r *http.Request, req chatRequest, providerName string) {
	text := lastUserText(req)

	if testserver.ErrorScenario(w, text) {
		return
	}

	if req.Stream {
		flusher, ok := testserver.StartSSE(t, w)
		if !ok {
			return
		}

		switch text {
		case "stream simple":
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}})
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "lo!"}}}})
			stop := "stop"
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{FinishReason: &stop}}})
			writeSSE(w, flusher, chatStreamChunk{Usage: &wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "stream tool":
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
				ToolCalls: []wireToolCallDelta{{Index: 0, ID: "call_1", Type: "function", Function: wireToolCallFunc{Name: "get_weather", Arguments: ""}}},
			}}}})
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
				ToolCalls: []wireToolCallDelta{{Index: 0, Function: wireToolCallFunc{Arguments: `{"city":`}}},
			}}}})
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{
				ToolCalls: []wireToolCallDelta{{Index: 0, Function: wireToolCallFunc{Arguments: `"Ghent"}`}}},
			}}}})
			toolCalls := "tool_calls"
			writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{FinishReason: &toolCalls}}})
			writeSSE(w, flusher, chatStreamChunk{Usage: &wireUsage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12}})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			testserver.SSEUnknownScenario(t, w, flusher, text)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch text {
	case "simple":
		content := fmt.Sprintf("Hello from %s!", providerName)
		resp := chatResponse{
			Choices: []chatResponseChoice{{
				Message:      chatResponseMessage{Content: &content},
				FinishReason: "stop",
			}},
			Usage: wireUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
		}
		_ = json.NewEncoder(w).Encode(resp)
	case "tool":
		resp := chatResponse{
			Choices: []chatResponseChoice{{
				Message: chatResponseMessage{
					ToolCalls: []wireToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: wireToolCallFunc{
							Name:      "get_weather",
							Arguments: `{"city":"Ghent"}`,
						},
					}},
				},
				FinishReason: "tool_calls",
			}},
			Usage: wireUsage{PromptTokens: 6, CompletionTokens: 4, TotalTokens: 10},
		}
		_ = json.NewEncoder(w).Encode(resp)
	default:
		testserver.JSONUnknownScenario(t, w, text)
	}
}

func embedImpl(w http.ResponseWriter, req embeddingRequest) {
	data := make([]embeddingData, len(req.Input))
	total := 0
	for i, v := range req.Input {
		data[i] = embeddingData{
			Index:     i,
			Embedding: []float64{float64(i), float64(len(v))},
		}
		total += len(v)
	}

	resp := embeddingResponse{
		Data:  data,
		Usage: wireUsage{PromptTokens: total, TotalTokens: total},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
