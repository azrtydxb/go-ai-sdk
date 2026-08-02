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
package compattest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

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
// and embeddings wire format. Callers must Close it (or rely on the
// t.Cleanup registered by NewFixtureServer).
type Server struct {
	*httptest.Server

	mu          sync.Mutex
	requests    [][]byte
	authHeaders []string
}

// Requests returns the raw JSON bodies of every chat/embeddings request
// received so far, in arrival order.
func (s *Server) Requests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.requests))
	copy(out, s.requests)
	return out
}

// AuthHeaders returns the Authorization header value of every
// chat/embeddings request received so far, in arrival order (one entry per
// Requests() entry).
func (s *Server) AuthHeaders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.authHeaders))
	copy(out, s.authHeaders)
	return out
}

func (s *Server) record(raw []byte, auth string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, raw)
	s.authHeaders = append(s.authHeaders, auth)
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("compattest: read body: %v", err)
	}
	return buf
}

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

func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// NewFixtureServer returns an httptest.Server speaking the OpenAI
// chat-completions + embeddings wire format, implementing the six
// providertest scenarios keyed off the LAST user message text, with
// "simple" responding "Hello from <providerName>!". Callers must Close it
// (handled automatically via t.Cleanup).
func NewFixtureServer(t *testing.T, providerName string) *Server {
	t.Helper()
	s := &Server{}

	mux := http.NewServeMux()

	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("compattest: decode request: %v", err)
		}
		s.record(raw, r.Header.Get("Authorization"))

		text := lastUserText(req)

		switch text {
		case "fail 429":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		case "fail 400":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"bad request"}}`))
			return
		}

		if req.Stream {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("compattest: ResponseWriter does not support flushing")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			switch text {
			case "stream simple":
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}})
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "lo!"}}}})
				stop := "stop"
				writeSSE(w, flusher, chatStreamChunk{Choices: []chatStreamChoice{{FinishReason: &stop}}})
				writeSSE(w, flusher, chatStreamChunk{Usage: &wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}})
				fmt.Fprint(w, "data: [DONE]\n\n")
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
				fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
			default:
				t.Fatalf("compattest: unknown streaming scenario %q", text)
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
			json.NewEncoder(w).Encode(resp)
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
			json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("compattest: unknown scenario %q", text)
		}
	})

	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("compattest: decode embedding request: %v", err)
		}
		s.record(raw, r.Header.Get("Authorization"))

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
		json.NewEncoder(w).Encode(resp)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.Server = srv
	return s
}
