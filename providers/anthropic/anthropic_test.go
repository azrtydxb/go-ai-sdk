package anthropic

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
	lastRequest messagesRequest
}

func (fs *fixtureState) record(raw []byte, req messagesRequest) {
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

// lastUserText extracts the text of the last user message's first text
// block, which the fixtures below always send as a single text block.
func lastUserText(req messagesRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		m := req.Messages[i]
		if m.Role != "user" {
			continue
		}
		for _, b := range m.Content {
			if b.Type == "text" {
				return b.Text
			}
		}
	}
	return ""
}

func writeNamedSSE(w http.ResponseWriter, flusher http.Flusher, event string, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

func readBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf
}

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureState) {
	t.Helper()
	fs := &fixtureState{}

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got == "" {
			t.Errorf("fixture: missing x-api-key header")
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Errorf("fixture: anthropic-version = %q, want %q", got, anthropicVersion)
		}

		var req messagesRequest
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
				writeNamedSSE(w, flusher, "message_start", messageStartEvent{})
				writeNamedSSE(w, flusher, "content_block_start", contentBlockStartEvent{
					Index:        0,
					ContentBlock: streamContentBlock{Type: "text"},
				})
				writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
					Index: 0, Delta: streamDelta{Type: "text_delta", Text: "Hel"},
				})
				writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
					Index: 0, Delta: streamDelta{Type: "text_delta", Text: "lo!"},
				})
				writeNamedSSE(w, flusher, "content_block_stop", contentBlockStopEvent{Index: 0})
				md := messageDeltaEvent{Usage: &messageDeltaUsage{OutputTokens: 2}}
				md.Delta.StopReason = "end_turn"
				writeNamedSSE(w, flusher, "message_delta", md)
				writeNamedSSE(w, flusher, "message_stop", struct{}{})
			case "stream tool":
				writeNamedSSE(w, flusher, "content_block_start", contentBlockStartEvent{
					Index: 0,
					ContentBlock: streamContentBlock{
						Type: "tool_use", ID: "call_1", Name: "get_weather",
					},
				})
				writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
					Index: 0, Delta: streamDelta{Type: "input_json_delta", PartialJSON: `{"city":`},
				})
				writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
					Index: 0, Delta: streamDelta{Type: "input_json_delta", PartialJSON: `"Ghent"}`},
				})
				writeNamedSSE(w, flusher, "content_block_stop", contentBlockStopEvent{Index: 0})
				md := messageDeltaEvent{Usage: &messageDeltaUsage{OutputTokens: 4}}
				md.Delta.StopReason = "tool_use"
				writeNamedSSE(w, flusher, "message_delta", md)
				writeNamedSSE(w, flusher, "message_stop", struct{}{})
			default:
				t.Fatalf("fixture: unknown streaming scenario %q", text)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch text {
		case "simple":
			resp := messageResponse{
				Content:    []wireContentBlock{{Type: "text", Text: "Hello from anthropic!"}},
				StopReason: "end_turn",
				Usage:      wireUsage{InputTokens: 5, OutputTokens: 3},
			}
			json.NewEncoder(w).Encode(resp)
		case "tool":
			resp := messageResponse{
				Content: []wireContentBlock{{
					Type: "tool_use", ID: "toolu_1", Name: "get_weather",
					Input: json.RawMessage(`{"city":"Ghent"}`),
				}},
				StopReason: "tool_use",
				Usage:      wireUsage{InputTokens: 6, OutputTokens: 4},
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
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("claude-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "anthropic"})
}

func TestCapabilities(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")
	if caps := model.Capabilities(); caps.NativeJSON {
		t.Errorf("Capabilities().NativeJSON = true, want false")
	}
}

func TestRequestShapeSystemExtraction(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.SystemText("You are helpful."),
			provider.SystemText("Be concise."),
			provider.UserText("simple"),
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.lastRequest
	if want := "You are helpful.\n\nBe concise."; req.System != want {
		t.Errorf("System = %q, want %q", req.System, want)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("Messages = %d, want 1 (system messages must not appear in messages[])", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want user", req.Messages[0].Role)
	}
}

func TestRequestShapeMaxTokensDefault(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var maxTokens int
	if err := json.Unmarshal(raw["max_tokens"], &maxTokens); err != nil {
		t.Fatalf("decode max_tokens: %v", err)
	}
	if maxTokens != defaultMaxTokens {
		t.Errorf("max_tokens = %d, want default %d", maxTokens, defaultMaxTokens)
	}
}

func TestRequestShapeMaxTokensExplicit(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	maxTokens := 123
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fs.lastRequest.MaxTokens != 123 {
		t.Errorf("MaxTokens = %d, want 123", fs.lastRequest.MaxTokens)
	}
}

func TestRequestShapeToolsAndSchema(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get the weather",
			Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(raw["tools"], &tools); err != nil {
		t.Fatalf("decode tools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(tools))
	}
	if _, ok := tools[0]["input_schema"]; !ok {
		t.Errorf("tool missing input_schema field: %v", tools[0])
	}
	var name string
	json.Unmarshal(tools[0]["name"], &name)
	if name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", name)
	}
}

func TestRequestShapeToolChoiceModes(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	cases := []struct {
		mode provider.ToolChoiceMode
		want string
	}{
		{provider.ToolChoiceAuto, `{"type":"auto"}`},
		{provider.ToolChoiceRequired, `{"type":"any"}`},
	}
	for _, tc := range cases {
		_, err := model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("simple")},
			Tools: []provider.ToolDef{{
				Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`),
			}},
			ToolChoice: &provider.ToolChoice{Mode: tc.mode},
		})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var raw map[string]json.RawMessage
		json.Unmarshal(fs.rawBody(), &raw)
		var got, want map[string]string
		json.Unmarshal(raw["tool_choice"], &got)
		json.Unmarshal([]byte(tc.want), &want)
		if got["type"] != want["type"] {
			t.Errorf("mode %v: tool_choice = %v, want %v", tc.mode, got, want)
		}
	}

	// tool mode
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var tc map[string]string
	json.Unmarshal(raw["tool_choice"], &tc)
	if tc["type"] != "tool" || tc["name"] != "get_weather" {
		t.Errorf("tool_choice = %v, want {type:tool, name:get_weather}", tc)
	}

	// none mode: tools must be omitted entirely
	_, err = model.Generate(context.Background(), provider.Call{
		Messages:   []provider.Message{provider.UserText("simple")},
		Tools:      []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &provider.ToolChoice{Mode: provider.ToolChoiceNone},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	raw = nil
	json.Unmarshal(fs.rawBody(), &raw)
	if _, ok := raw["tools"]; ok {
		t.Errorf("request has tools field with ToolChoiceNone, want omitted: %s", fs.rawBody())
	}
	if _, ok := raw["tool_choice"]; ok {
		t.Errorf("request has tool_choice field with ToolChoiceNone, want omitted: %s", fs.rawBody())
	}
}

func TestRequestShapeToolResultBlock(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.ToolResultPart{
					ToolCallID: "toolu_1",
					Result:     map[string]any{"temp": 42},
					IsError:    true,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.lastRequest
	if len(req.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2", len(req.Messages))
	}
	toolMsg := req.Messages[1]
	if toolMsg.Role != "user" {
		t.Errorf("tool result message Role = %q, want user", toolMsg.Role)
	}
	if len(toolMsg.Content) != 1 {
		t.Fatalf("tool result message Content = %d blocks, want 1", len(toolMsg.Content))
	}
	block := toolMsg.Content[0]
	if block.Type != "tool_result" {
		t.Errorf("block.Type = %q, want tool_result", block.Type)
	}
	if block.ToolUseID != "toolu_1" {
		t.Errorf("block.ToolUseID = %q, want toolu_1", block.ToolUseID)
	}
	if block.IsError == nil || !*block.IsError {
		t.Errorf("block.IsError = %v, want pointer to true", block.IsError)
	}

	// Confirm is_error is present in the raw JSON (not merely a Go zero
	// value that happens to match).
	var raw map[string]json.RawMessage
	json.Unmarshal(fs.rawBody(), &raw)
	var msgs []json.RawMessage
	json.Unmarshal(raw["messages"], &msgs)
	var lastMsg map[string]json.RawMessage
	json.Unmarshal(msgs[len(msgs)-1], &lastMsg)
	var blocks []map[string]json.RawMessage
	json.Unmarshal(lastMsg["content"], &blocks)
	if _, ok := blocks[0]["is_error"]; !ok {
		t.Errorf("raw tool_result block missing is_error key: %s", blocks[0])
	}
}

func TestRequestShapeToolMessageFilePartErrors(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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

func TestRequestShapeAssistantToolCallParsedInput(t *testing.T) {
	srv, fs := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{provider.ToolCallPart{
					ID:   "toolu_1",
					Name: "get_weather",
					Args: json.RawMessage(`{"city":"Ghent"}`),
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := fs.lastRequest
	block := req.Messages[1].Content[0]
	if block.Type != "tool_use" {
		t.Fatalf("block.Type = %q, want tool_use", block.Type)
	}
	var input map[string]string
	if err := json.Unmarshal(block.Input, &input); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if input["city"] != "Ghent" {
		t.Errorf("input.city = %q, want Ghent", input["city"])
	}
}

func TestGenerateToolResponseUsage(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("tool")},
		Tools:    []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Usage.TotalTokens != resp.Usage.InputTokens+resp.Usage.OutputTokens {
		t.Errorf("TotalTokens = %d, want sum of input+output", resp.Usage.TotalTokens)
	}
}

// streamSSEServer starts an httptest server that writes exactly the given
// raw named SSE events and then closes the response without a trailing
// "message_stop" event — simulating a proxy/load balancer that truncates
// the stream after forwarding real events.
func streamSSEServer(t *testing.T, events []namedEvent) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, e := range events {
			writeNamedSSE(w, flusher, e.name, e.payload)
		}
		// Deliberately no "message_stop" — the handler returns here,
		// closing the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

type namedEvent struct {
	name    string
	payload any
}

// TestStreamEndsWithoutMessageStopButHasStopReason covers the case where
// the server sends a message_delta with a stop_reason (and usage) but the
// connection closes before a "message_stop" event arrives. This must still
// yield exactly one FinishPart with Err() == nil.
func TestStreamEndsWithoutMessageStopButHasStopReason(t *testing.T) {
	md := messageDeltaEvent{Usage: &messageDeltaUsage{OutputTokens: 2}}
	md.Delta.StopReason = "end_turn"

	srv := streamSSEServer(t, []namedEvent{
		{"content_block_start", contentBlockStartEvent{Index: 0, ContentBlock: streamContentBlock{Type: "text"}}},
		{"content_block_delta", contentBlockDeltaEvent{Index: 0, Delta: streamDelta{Type: "text_delta", Text: "Hel"}}},
		{"content_block_delta", contentBlockDeltaEvent{Index: 0, Delta: streamDelta{Type: "text_delta", Text: "lo!"}}},
		{"content_block_stop", contentBlockStopEvent{Index: 0}},
		{"message_delta", md},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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
		t.Fatalf("Err() = %v, want nil (stop_reason was received before truncation)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.OutputTokens != 2 {
		t.Errorf("FinishPart.Usage.OutputTokens = %d, want 2", finishes[0].Usage.OutputTokens)
	}
}

// TestStreamTruncatedBeforeStopReason covers the case where the connection
// closes before any message_delta with a stop_reason arrives at all — a
// true mid-response truncation. This must yield zero FinishParts and a
// non-nil Err().
func TestStreamTruncatedBeforeStopReason(t *testing.T) {
	srv := streamSSEServer(t, []namedEvent{
		{"content_block_start", contentBlockStartEvent{Index: 0, ContentBlock: streamContentBlock{Type: "text"}}},
		{"content_block_delta", contentBlockDeltaEvent{Index: 0, Delta: streamDelta{Type: "text_delta", Text: "Hel"}}},
	})
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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
		t.Fatal("Err() = nil, want non-nil (stream truncated before stop_reason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}

func TestStreamClosedTwiceIsIdempotent(t *testing.T) {
	srv, _ := newFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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
