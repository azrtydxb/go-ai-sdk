package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

// fixtureState records the last request the fixture server saw, so tests
// can assert on wire-level request shape.
type fixtureState struct {
	mu          sync.Mutex
	lastRawBody []byte
	lastRequest chatRequest
}

func (fs *fixtureState) record(raw []byte, req chatRequest) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.lastRawBody = raw
	fs.lastRequest = req
}

func (fs *fixtureState) rawBody() []byte {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastRawBody
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

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureState) {
	t.Helper()
	fs := &fixtureState{}

	mux := http.NewServeMux()

	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}
		fs.record(raw, req)

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
				t.Fatalf("fixture: ResponseWriter does not support flushing")
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
				t.Fatalf("fixture: unknown streaming scenario %q", text)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch text {
		case "simple":
			content := "Hello from openai!"
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
			t.Fatalf("fixture: unknown scenario %q", text)
		}
	})

	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		raw := readBody(t, r)
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("fixture: decode embedding request: %v", err)
		}

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
	return srv, fs
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf
}

func TestConformance(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("gpt-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "openai"})
}

func TestRequestShapeTools(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	maxTokens := 42
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get the weather",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object"}`),
		},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	if _, ok := raw["tools"]; !ok {
		t.Errorf("request missing tools field: %s", fs.rawBody())
	}
	var tools []wireTool
	json.Unmarshal(raw["tools"], &tools)
	if len(tools) != 1 || tools[0].Type != "function" || tools[0].Function.Name != "get_weather" {
		t.Errorf("tools = %+v, want one function tool named get_weather", tools)
	}

	var toolChoice wireToolChoiceObj
	if err := json.Unmarshal(raw["tool_choice"], &toolChoice); err != nil {
		t.Fatalf("decode tool_choice: %v", err)
	}
	if toolChoice.Type != "function" || toolChoice.Function.Name != "get_weather" {
		t.Errorf("tool_choice = %+v, want {function, get_weather}", toolChoice)
	}

	var rf wireJSONSchemaFormat
	if err := json.Unmarshal(raw["response_format"], &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if rf.Type != "json_schema" || rf.JSONSchema == nil || rf.JSONSchema.Name != "weather_schema" || !rf.JSONSchema.Strict {
		t.Errorf("response_format = %+v, want json_schema/weather_schema/strict", rf)
	}

	var mct int
	if err := json.Unmarshal(raw["max_completion_tokens"], &mct); err != nil {
		t.Fatalf("decode max_completion_tokens: %v", err)
	}
	if mct != 42 {
		t.Errorf("max_completion_tokens = %d, want 42", mct)
	}
}

func TestRequestShapeToolChoiceModes(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	cases := []struct {
		mode provider.ToolChoiceMode
		want string
	}{
		{provider.ToolChoiceAuto, `"auto"`},
		{provider.ToolChoiceNone, `"none"`},
		{provider.ToolChoiceRequired, `"required"`},
	}
	for _, tc := range cases {
		_, err := model.Generate(context.Background(), provider.Call{
			Messages:   []provider.Message{provider.UserText("simple")},
			ToolChoice: &provider.ToolChoice{Mode: tc.mode},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var raw map[string]json.RawMessage
		json.Unmarshal(fs.rawBody(), &raw)
		if string(raw["tool_choice"]) != tc.want {
			t.Errorf("mode %v: tool_choice = %s, want %s", tc.mode, raw["tool_choice"], tc.want)
		}
	}
}

func TestRequestShapeJSONObjectResponseFormat(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "json"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var rf map[string]string
	json.Unmarshal(raw["response_format"], &rf)
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", rf["type"])
	}
}

func TestRequestShapeUnsupportedResponseFormatType(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "xml"},
	})
	if err == nil {
		t.Fatal("Generate: want error for unsupported ResponseFormat.Type, got nil")
	}
}

// streamSSEServer starts an httptest server that writes exactly the given
// raw SSE chunks (JSON-encoded) as "data: ..." events and then closes the
// response without a trailing "data: [DONE]" — simulating a proxy/load
// balancer that truncates the stream after forwarding real chunks.
func streamSSEServer(t *testing.T, chunks ...chatStreamChunk) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			writeSSE(w, flusher, c)
		}
		// Deliberately no "data: [DONE]" — the handler returns here,
		// closing the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamEndsWithoutDoneButHasFinishReason covers the case where the
// server sends a finish_reason chunk (and usage) but the connection closes
// before a "data: [DONE]" sentinel arrives. Per the documented rule in
// streamResponse.Parts, this must still yield exactly one FinishPart with
// Err() == nil.
func TestStreamEndsWithoutDoneButHasFinishReason(t *testing.T) {
	stop := "stop"
	srv := streamSSEServer(t,
		chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}},
		chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "lo!"}}}},
		chatStreamChunk{Choices: []chatStreamChoice{{FinishReason: &stop}}},
		chatStreamChunk{Usage: &wireUsage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
	)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (finish_reason was received before truncation)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.TotalTokens != 7 {
		t.Errorf("FinishPart.Usage.TotalTokens = %d, want 7", finishes[0].Usage.TotalTokens)
	}
}

// TestStreamTruncatedBeforeFinishReason covers the case where the
// connection closes before any finish_reason chunk arrives at all — a true
// mid-response truncation. This must yield zero FinishParts and a non-nil
// Err().
func TestStreamTruncatedBeforeFinishReason(t *testing.T) {
	srv := streamSSEServer(t,
		chatStreamChunk{Choices: []chatStreamChoice{{Delta: chatStreamDelta{Content: "Hel"}}}},
	)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err == nil {
		t.Fatal("Err() = nil, want non-nil (stream truncated before finish_reason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}
