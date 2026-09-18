package codextransport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// makeFakeJWT builds a JWT-like string with a fake payload containing
// the chatgpt_account_id claim. No signature is needed — we only use
// it to verify header parsing and claim extraction in tests.
func makeFakeJWT(accountID string) string {
	header := `{"alg":"none","typ":"JWT"}`
	h := base64.RawURLEncoding.EncodeToString([]byte(header))
	payload, _ := json.Marshal(map[string]any{
		"sub": "user123",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	p := base64.RawURLEncoding.EncodeToString(payload)
	return h + "." + p + "."
}

func testCred(accountID string) codexauth.Credential {
	return codexauth.Credential{
		Access:    makeFakeJWT(accountID),
		Refresh:   "refresh-test",
		Expires:   fakeTime,
		AccountID: accountID,
	}
}

var fakeTime = time.Now().Add(24 * time.Hour)

func TestReasoningEffortFallback(t *testing.T) {
	for _, reasoning := range []*provider.ReasoningConfig{nil, {}, {Effort: "max"}} {
		for _, stream := range []bool{false, true} {
			m := NewModelWithOptions(Config{Credential: testCred("account"), ModelID: "test", ReasoningEffort: "low"}).(*languageModel)
			req, err := m.buildRequest(t.Context(), provider.Call{Reasoning: reasoning}, stream)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Reasoning struct{ Effort, Summary string }
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			_ = req.Body.Close()
			want := "low"
			if reasoning != nil && reasoning.Effort != "" {
				want = reasoning.Effort
			}
			if body.Reasoning.Effort != want || body.Reasoning.Summary != "auto" {
				t.Fatalf("reasoning=%+v, want effort %q with existing summary", body.Reasoning, want)
			}
		}
	}
}

// sseFixtureServer creates an httptest.Server that responds with the
// given raw SSE events (already JSON-encoded). The events must follow
// the format "data: <event>\n\n".
func sseFixtureServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("missing Bearer auth, got %q", auth)
		}
		if acct := r.Header.Get("chatgpt-account-id"); acct == "" {
			t.Error("missing chatgpt-account-id header")
		}
		if beta := r.Header.Get("OpenAI-Beta"); beta != "responses=experimental" {
			t.Errorf("OpenAI-Beta = %q, want responses=experimental", beta)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	}))
}

// TestBuildRequest verifies the request shape (headers, body structure).
func TestBuildRequest(t *testing.T) {
	cred := testCred("acc-123")
	cfg := Config{
		Credential:   cred,
		ModelID:      "gpt-test",
		BaseURL:      defaultCodexBaseURL,
		Instructions: "You are a test assistant.",
	}
	m := NewModelWithOptions(cfg)

	call := provider.Call{
		Messages: []provider.Message{provider.UserText("hello")},
	}

	req, err := m.(*languageModel).buildRequest(context.Background(), call, true)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	if req.Header.Get("Authorization") != "Bearer "+cred.Access {
		t.Errorf("Authorization = %q, want %q", req.Header.Get("Authorization"), "Bearer "+cred.Access)
	}
	if req.Header.Get("chatgpt-account-id") != "acc-123" {
		t.Errorf("chatgpt-account-id = %q, want acc-123", req.Header.Get("chatgpt-account-id"))
	}
	if req.Header.Get("originator") != defaultOriginator {
		t.Errorf("originator = %q, want %q", req.Header.Get("originator"), defaultOriginator)
	}

	// Verify body contains required fields.
	body := readBody(t, req.Body)
	var b map[string]any
	if err := json.Unmarshal(body, &b); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if b["model"] != "gpt-test" {
		t.Errorf("model = %v, want gpt-test", b["model"])
	}
	if b["stream"] != true {
		t.Errorf("stream = %v, want true", b["stream"])
	}
	input, ok := b["input"].([]any)
	if !ok {
		t.Fatal("input not present")
	}
	if len(input) == 0 {
		t.Fatal("input is empty")
	}
	// First item should be a user message.
	first, ok := input[0].(map[string]any)
	if !ok {
		t.Fatal("first input item not a map")
	}
	if first["role"] != "user" {
		t.Errorf("input[0].role = %v, want user", first["role"])
	}
}

func readBody(t *testing.T, body io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	return data
}

// TestStreamResponse_parse tests the SSE stream parsing for a simple
// text response.
func TestStreamResponse_parseSimpleText(t *testing.T) {
	chunks := []string{
		`{"type":"response.created","response":{"system_fingerprint":"fp-123"}}`,
		`{"type":"response.output_item.added","item":{"id":"fc_1","type":"message"}}`,
		`{"type":"response.content_part.added","item_id":"fc_1"}`,
		`{"type":"response.delta","delta":{"type":"delta","text":"Hello"}}`,
		`{"type":"response.delta","delta":{"type":"delta","text":" World"}}`,
		`{"type":"response.output_item.done","item":{"id":"fc_1","type":"message"}}`,
		`{"type":"response.completed","stop_reason":"stop","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
	}

	srv := sseFixtureServer(t, chunks...)
	cred := testCred("acc-123")
	cfg := Config{
		Credential: cred,
		ModelID:    "gpt-test",
		BaseURL:    srv.URL,
	}
	m := NewModelWithOptions(cfg)

	sr, err := m.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var textParts []provider.TextDelta
	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		switch p := part.(type) {
		case provider.TextDelta:
			textParts = append(textParts, p)
		case provider.FinishPart:
			finishes = append(finishes, p)
		}
	}

	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}

	// Should have two text deltas.
	if len(textParts) != 2 {
		t.Errorf("got %d TextDelta, want 2: %+v", len(textParts), textParts)
	}

	// Concatenated text should be "Hello World".
	var fullText strings.Builder
	for _, td := range textParts {
		fullText.WriteString(td.Text)
	}
	if fullText.String() != "Hello World" {
		t.Errorf("concatenated text = %q, want Hello World", fullText.String())
	}

	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart, want 1", len(finishes))
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishReason = %q, want stop", finishes[0].Reason)
	}
	if finishes[0].Usage.TotalTokens != 7 {
		t.Errorf("Usage.TotalTokens = %d, want 7", finishes[0].Usage.TotalTokens)
	}
	if finishes[0].ProviderMetadata == nil {
		t.Error("ProviderMetadata is nil, want system_fingerprint")
	}
}

// TestStreamResponse_parseToolCall tests stream parsing with a tool call.
func TestStreamResponse_parseToolCall(t *testing.T) {
	chunks := []string{
		`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","name":"get_weather"}}`,
		`{"type":"response.delta","delta":{"type":"input_delta","text":"{\"city\""}}`,
		`{"type":"response.delta","delta":{"type":"input_delta","text":": \"Ghent\"}"}}`,
		`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call"}}`,
		`{"type":"response.completed","stop_reason":"tool_calls"}`,
	}

	srv := sseFixtureServer(t, chunks...)
	cred := testCred("acc-123")
	cfg := Config{
		Credential: cred,
		ModelID:    "gpt-test",
		BaseURL:    srv.URL,
	}
	m := NewModelWithOptions(cfg)

	sr, err := m.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("tool test")},
		Tools: []provider.ToolDef{{
			Name:        "get_weather",
			Description: "Get weather",
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var toolEnds []provider.ToolCallEnd
	for part := range sr.Parts() {
		if end, ok := part.(provider.ToolCallEnd); ok {
			toolEnds = append(toolEnds, end)
		}
	}

	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(toolEnds) != 1 {
		t.Fatalf("got %d ToolCallEnd, want 1", len(toolEnds))
	}
	if toolEnds[0].Call.Name != "get_weather" {
		t.Errorf("ToolCallEnd.Call.Name = %q, want get_weather", toolEnds[0].Call.Name)
	}
}

func TestBuildRequestToolResultDoesNotPanicOnInvalidContent(t *testing.T) {
	m := NewModelWithOptions(Config{
		Credential: testCred("acc-123"),
		ModelID:    "gpt-test",
		BaseURL:    defaultCodexBaseURL,
	})

	_, err := m.(*languageModel).buildRequest(context.Background(), provider.Call{
		Messages: []provider.Message{{
			Role:    provider.RoleTool,
			Content: []provider.ContentPart{provider.TextPart{Text: "not a tool result"}},
		}},
	}, true)
	if err == nil {
		t.Fatal("buildRequest error = nil, want missing ToolResultPart error")
	}
	if !strings.Contains(err.Error(), "missing ToolResultPart") {
		t.Fatalf("buildRequest error = %v, want missing ToolResultPart", err)
	}
}

func TestBuildRequestToolResultUsesToolResultPart(t *testing.T) {
	m := NewModelWithOptions(Config{
		Credential: testCred("acc-123"),
		ModelID:    "gpt-test",
		BaseURL:    defaultCodexBaseURL,
	})

	req, err := m.(*languageModel).buildRequest(context.Background(), provider.Call{
		Messages: []provider.Message{{
			Role: provider.RoleTool,
			Content: []provider.ContentPart{
				provider.TextPart{Text: "ignored text"},
				provider.ToolResultPart{ToolCallID: "call_123", Result: map[string]any{"ok": true}},
			},
		}},
	}, true)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(readBody(t, req.Body), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	input := body["input"].([]any)
	item := input[0].(map[string]any)
	if item["call_id"] != "call_123" {
		t.Fatalf("call_id = %v, want call_123", item["call_id"])
	}
	if !strings.Contains(item["result"].(string), "ok") {
		t.Fatalf("result = %v, want marshaled tool result", item["result"])
	}
}

func TestStreamResponse_parsePiShapedResponsesEvents(t *testing.T) {
	chunks := []string{
		`{"type":"response.created","response":{"id":"resp_1","system_fingerprint":"fp-123"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message"}}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
		`{"type":"response.output_text.delta","output_index":0,"delta":" Pi"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","content":[{"type":"output_text","text":"Hello Pi"}]}}`,
		`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"cached_input_tokens_read":1}}}`,
	}

	srv := sseFixtureServer(t, chunks...)
	defer srv.Close()
	m := NewModelWithOptions(Config{Credential: testCred("acc-123"), ModelID: "gpt-test", BaseURL: srv.URL})

	sr, err := m.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("hello")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var text strings.Builder
	var finish provider.FinishPart
	for part := range sr.Parts() {
		switch p := part.(type) {
		case provider.TextDelta:
			text.WriteString(p.Text)
		case provider.FinishPart:
			finish = p
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if text.String() != "Hello Pi" {
		t.Fatalf("text = %q, want Hello Pi", text.String())
	}
	if finish.Reason != provider.FinishStop {
		t.Fatalf("finish reason = %q, want stop", finish.Reason)
	}
	if finish.Usage.CachedInputTokens != 1 {
		t.Fatalf("cached input tokens = %d, want 1", finish.Usage.CachedInputTokens)
	}
}

func TestStreamResponse_parsePiShapedToolCall(t *testing.T) {
	chunks := []string{
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"get_weather"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\""}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"city\":\"Ghent\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"get_weather","arguments":"{\"city\":\"Ghent\"}"}}`,
		`{"type":"response.completed","response":{"status":"completed"}}`,
	}

	srv := sseFixtureServer(t, chunks...)
	defer srv.Close()
	m := NewModelWithOptions(Config{Credential: testCred("acc-123"), ModelID: "gpt-test", BaseURL: srv.URL})

	sr, err := m.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("hello")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var end provider.ToolCallEnd
	for part := range sr.Parts() {
		if p, ok := part.(provider.ToolCallEnd); ok {
			end = p
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if end.Call.Name != "get_weather" {
		t.Fatalf("tool name = %q, want get_weather", end.Call.Name)
	}
	if string(end.Call.Args) != `{"city":"Ghent"}` {
		t.Fatalf("tool args = %s, want city Ghent", end.Call.Args)
	}
}

// TestNonStreamingResponse tests non-streaming response parsing.
func TestParseNonStreamingResponse(t *testing.T) {
	body := `{
		"id": "resp-123",
		"model": "gpt-test",
		"stop_reason": "stop",
		"usage": {"input_tokens": 5, "output_tokens": 2, "total_tokens": 7, "cached_input_tokens_read": 1},
		"output": [{
			"id": "fc_1",
			"type": "message",
			"role": "assistant",
			"content": [
				{"type": "output_text", "text": "Hello from Codex!"}
			]
		}]
	}`

	resp, err := parseNonStreamingResponse([]byte(body))
	if err != nil {
		t.Fatalf("parseNonStreamingResponse: %v", err)
	}
	if resp.Text() != "Hello from Codex!" {
		t.Errorf("Text() = %q, want Hello from Codex!", resp.Text())
	}
	if resp.FinishReason != provider.FinishStop {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.Usage.TotalTokens != 7 {
		t.Errorf("Usage.TotalTokens = %d, want 7", resp.Usage.TotalTokens)
	}
	if resp.Usage.CachedInputTokens != 1 {
		t.Errorf("Usage.CachedInputTokens = %d, want 1", resp.Usage.CachedInputTokens)
	}
}
