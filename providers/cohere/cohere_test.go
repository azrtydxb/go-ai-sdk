package cohere

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func (fs *fixtureState) request() chatRequest {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastRequest
}

// lastUserText extracts the text of the last user message.
func lastUserText(req chatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		return m.Content
	}
	return ""
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func idxPtr(i int) *int { return &i }

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureState) {
	t.Helper()
	fs := &fixtureState{}

	mux := http.NewServeMux()

	mux.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("fixture: Authorization header = %q, want Bearer prefix", got)
		}

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
			w.Write([]byte(`{"message":"rate limited"}`))
			return
		case "fail 400":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"bad request"}`))
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
				writeSSE(w, flusher, streamEvent{Type: "message-start"})
				writeSSE(w, flusher, streamEvent{Type: "content-start", Index: idxPtr(0)})
				writeSSE(w, flusher, streamEvent{Type: "content-delta", Index: idxPtr(0), Delta: &streamDelta{
					Message: &streamMessage{Content: &streamContent{Text: "Hel"}},
				}})
				writeSSE(w, flusher, streamEvent{Type: "content-delta", Index: idxPtr(0), Delta: &streamDelta{
					Message: &streamMessage{Content: &streamContent{Text: "lo!"}},
				}})
				writeSSE(w, flusher, streamEvent{Type: "content-end", Index: idxPtr(0)})
				writeSSE(w, flusher, streamEvent{Type: "message-end", Delta: &streamDelta{
					FinishReason: "COMPLETE",
					Usage:        &streamUsage{Tokens: chatResponseTokens{InputTokens: 5, OutputTokens: 2}},
				}})
				flusher.Flush()
			case "stream tool":
				writeSSE(w, flusher, streamEvent{Type: "message-start"})
				writeSSE(w, flusher, streamEvent{Type: "tool-plan-delta", Delta: &streamDelta{}})
				writeSSE(w, flusher, streamEvent{Type: "tool-call-start", Index: idxPtr(0), Delta: &streamDelta{
					Message: &streamMessage{ToolCalls: &streamToolCall{
						ID: "call_1", Type: "function",
						Function: &streamToolCallFunc{Name: "get_weather"},
					}},
				}})
				writeSSE(w, flusher, streamEvent{Type: "tool-call-delta", Index: idxPtr(0), Delta: &streamDelta{
					Message: &streamMessage{ToolCalls: &streamToolCall{
						Function: &streamToolCallFunc{Arguments: `{"city":`},
					}},
				}})
				writeSSE(w, flusher, streamEvent{Type: "tool-call-delta", Index: idxPtr(0), Delta: &streamDelta{
					Message: &streamMessage{ToolCalls: &streamToolCall{
						Function: &streamToolCallFunc{Arguments: `"Ghent"}`},
					}},
				}})
				writeSSE(w, flusher, streamEvent{Type: "tool-call-end", Index: idxPtr(0)})
				writeSSE(w, flusher, streamEvent{Type: "message-end", Delta: &streamDelta{
					FinishReason: "TOOL_CALL",
					Usage:        &streamUsage{Tokens: chatResponseTokens{InputTokens: 6, OutputTokens: 4}},
				}})
				flusher.Flush()
			default:
				t.Fatalf("fixture: unknown streaming scenario %q", text)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch text {
		case "simple":
			resp := chatResponse{
				Message: &chatResponseMessage{
					Content: []chatResponseContent{{Type: "text", Text: "Hello from cohere!"}},
				},
				FinishReason: "COMPLETE",
				Usage:        chatResponseUsage{Tokens: chatResponseTokens{InputTokens: 5, OutputTokens: 3}},
			}
			json.NewEncoder(w).Encode(resp)
		case "tool":
			resp := chatResponse{
				Message: &chatResponseMessage{
					ToolCalls: []wireToolCall{{
						ID:       "call_1",
						Type:     "function",
						Function: wireToolCallFunc{Name: "get_weather", Arguments: `{"city":"Ghent"}`},
					}},
				},
				FinishReason: "TOOL_CALL",
				Usage:        chatResponseUsage{Tokens: chatResponseTokens{InputTokens: 6, OutputTokens: 4}},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			t.Fatalf("fixture: unknown scenario %q", text)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fs
}

func TestConformance(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("cohere-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "cohere"})
}

func TestCapabilities(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")
	if caps := model.Capabilities(); !caps.NativeJSON {
		t.Errorf("Capabilities().NativeJSON = false, want true")
	}
}

func TestRequestShapeTopPFieldIsP(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	topP := 0.42
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		TopP:     &topP,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	if _, ok := raw["top_p"]; ok {
		t.Errorf("request has top_p field, want only p: %s", fs.rawBody())
	}
	var got float64
	if err := json.Unmarshal(raw["p"], &got); err != nil {
		t.Fatalf("decode p: %v", err)
	}
	if got != 0.42 {
		t.Errorf("p = %v, want 0.42", got)
	}
}

func TestRequestShapeResponseFormatWithSchema(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var rf map[string]json.RawMessage
	if err := json.Unmarshal(raw["response_format"], &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	var typ string
	json.Unmarshal(rf["type"], &typ)
	if typ != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", typ)
	}
	if _, ok := rf["schema"]; !ok {
		t.Errorf("response_format has no schema field, want schema included (Cohere supports it): %s", raw["response_format"])
	} else {
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(rf["schema"], &schema); err != nil {
			t.Fatalf("decode schema: %v", err)
		}
		if _, ok := schema["properties"]; !ok {
			t.Errorf("schema missing properties: %s", rf["schema"])
		}
	}
}

func TestRequestShapeUnsupportedResponseFormatType(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{Type: "xml"},
	})
	if err == nil {
		t.Fatal("Generate: want error for unsupported ResponseFormat.Type, got nil")
	}
}

func TestRequestShapeToolMessageFilePartErrors(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.FilePart{
					Data:      []byte("data"),
					MediaType: "application/pdf",
				}},
			},
		},
	})
	if err == nil {
		t.Fatal("Generate: want error for FilePart in tool message, got nil")
	}
	if !strings.Contains(err.Error(), "FilePart") {
		t.Errorf("error = %q, want it to mention FilePart", err.Error())
	}
}

func TestRequestShapeToolResultOneMessagePerPart(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{
					provider.ToolResultPart{ToolCallID: "call_1", Name: "get_weather", Result: map[string]any{"temp": 42}},
					provider.ToolResultPart{ToolCallID: "call_2", Name: "get_time", Result: map[string]any{"time": "noon"}, IsError: true},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.request()
	if len(req.Messages) != 3 {
		t.Fatalf("Messages = %d, want 3 (user + one tool message per ToolResultPart)", len(req.Messages))
	}
	if req.Messages[1].Role != "tool" || req.Messages[1].ToolCallID != "call_1" {
		t.Errorf("Messages[1] = %+v, want role tool, tool_call_id call_1", req.Messages[1])
	}
	if req.Messages[1].Content != `{"temp":42}` {
		t.Errorf("Messages[1].Content = %q, want %q", req.Messages[1].Content, `{"temp":42}`)
	}
	if req.Messages[2].Role != "tool" || req.Messages[2].ToolCallID != "call_2" {
		t.Errorf("Messages[2] = %+v, want role tool, tool_call_id call_2", req.Messages[2])
	}
}

func TestRequestShapeToolChoiceToolSendsOnlyThatTool(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Tools: []provider.ToolDef{
			{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "get_time", Schema: json.RawMessage(`{"type":"object"}`)},
		},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.request()
	if len(req.Tools) != 1 {
		t.Fatalf("Tools = %d, want exactly 1 (only the chosen tool)", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tools[0].Function.Name = %q, want get_weather", req.Tools[0].Function.Name)
	}
	if req.ToolChoice != "REQUIRED" {
		t.Errorf("ToolChoice = %q, want REQUIRED", req.ToolChoice)
	}
}

func TestRequestShapeToolChoiceNoneOmitsTools(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceNone},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.request()
	if len(req.Tools) != 0 {
		t.Errorf("Tools = %d, want 0 (ToolChoiceNone omits tools)", len(req.Tools))
	}
}

func TestRequestShapeToolChoiceAutoSendsToolsAsIsNoField(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceAuto},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req := fs.request()
	if len(req.Tools) != 1 {
		t.Errorf("Tools = %d, want 1 (sent as-is)", len(req.Tools))
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	if _, ok := raw["tool_choice"]; ok {
		t.Errorf("request has tool_choice field, want none for auto mode")
	}
}

func TestRequestShapeToolChoiceRequiredSendsREQUIRED(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceRequired},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req := fs.request()
	if len(req.Tools) != 1 {
		t.Errorf("Tools = %d, want 1 (sent as-is)", len(req.Tools))
	}
	if req.ToolChoice != "REQUIRED" {
		t.Errorf("ToolChoice = %q, want REQUIRED", req.ToolChoice)
	}
}

func TestRequestShapeToolChoiceToolUnmatchedNameErrors(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "does_not_exist"},
	})
	if err == nil {
		t.Fatal("Generate: want error for unmatched tool choice name, got nil")
	}
	if !strings.Contains(err.Error(), `"does_not_exist"`) {
		t.Errorf("error = %q, want it to mention the unmatched tool name", err.Error())
	}
}

// rawSSEServer starts an httptest server that writes exactly the given raw
// SSE "data: ..." payloads and then closes the response — simulating a
// proxy/load balancer that truncates the stream.
func rawSSEServer(t *testing.T, events []streamEvent) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, e := range events {
			writeSSE(w, flusher, e)
		}
		// Handler returns here, closing the response body from the
		// client's point of view — no message-end sentinel in either case
		// tested below.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamEndsWithoutMessageEndButFinishReasonNeverSeen mirrors the
// "truncated mid-response" case: the connection closes before any
// message-end event arrives, so finish_reason was never seen.
func TestStreamTruncatedBeforeMessageEnd(t *testing.T) {
	srv := rawSSEServer(t, []streamEvent{
		{Type: "message-start"},
		{Type: "content-delta", Index: idxPtr(0), Delta: &streamDelta{
			Message: &streamMessage{Content: &streamContent{Text: "Hel"}},
		}},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

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
		t.Fatal("Err() = nil, want non-nil (stream truncated before message-end)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}

// TestStreamWithMessageEndIsWellFormed covers the well-formed case: a
// message-end event arrives (carrying finish_reason and usage) and the
// connection then closes normally. This must yield exactly one FinishPart
// with Err() == nil — Cohere has no separate [DONE] sentinel, so
// message-end alone is both the finish signal and the stream terminator.
func TestStreamWithMessageEndIsWellFormed(t *testing.T) {
	srv := rawSSEServer(t, []streamEvent{
		{Type: "message-start"},
		{Type: "content-delta", Index: idxPtr(0), Delta: &streamDelta{
			Message: &streamMessage{Content: &streamContent{Text: "Hel"}},
		}},
		{Type: "content-delta", Index: idxPtr(0), Delta: &streamDelta{
			Message: &streamMessage{Content: &streamContent{Text: "lo!"}},
		}},
		{Type: "message-end", Delta: &streamDelta{
			FinishReason: "COMPLETE",
			Usage:        &streamUsage{Tokens: chatResponseTokens{InputTokens: 5, OutputTokens: 2}},
		}},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

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
		t.Fatalf("Err() = %v, want nil (message-end was received)", err)
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

func TestStreamClosedTwiceIsIdempotent(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("cohere-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream simple")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range sr.Parts() {
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #1 = %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #2 = %v, want nil (idempotent)", err)
	}
}
