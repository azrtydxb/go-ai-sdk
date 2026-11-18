package openaicompat

// Tests for the OpenAI-wire parity fixes: verbatim tool results and null
// assistant content (#74), usage presence (#75), lenient streaming (#77)
// and embeddings without data[].index (#78). All servers are httptest.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// recordServer answers every request with body (content type ct) and
// records the request bodies.
func recordServer(t *testing.T, ct, body string) (*httptest.Server, *[][]byte) {
	t.Helper()
	var reqs [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs = append(reqs, b)
		w.Header().Set("Content-Type", ct)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

const okChat = `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`

func drain(t *testing.T, s provider.StreamResponse) []provider.StreamPart {
	t.Helper()
	var parts []provider.StreamPart
	for p := range s.Parts() {
		parts = append(parts, p)
	}
	return parts
}

func toolHistory(result any) []provider.Message {
	return []provider.Message{
		provider.UserText("go"),
		{Role: provider.RoleAssistant, Content: []provider.ContentPart{
			provider.ToolCallPart{ID: "c1", Name: "f", Args: json.RawMessage(`{"z":1,"a":2}`)},
		}},
		{Role: provider.RoleTool, Content: []provider.ContentPart{
			provider.ToolResultPart{ToolCallID: "c1", Name: "f", Result: result},
		}},
	}
}

type sentMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func sentMessages(t *testing.T, body []byte) []sentMsg {
	t.Helper()
	var req struct {
		Messages []sentMsg `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req.Messages
}

func TestRawContentToolResultIsSentVerbatim(t *testing.T) {
	srv, reqs := recordServer(t, "application/json", okChat)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")

	if _, err := model.Generate(context.Background(), provider.Call{
		Messages: toolHistory(provider.RawContent(`{"a":1}`)),
	}); err != nil {
		t.Fatal(err)
	}
	msgs := sentMessages(t, (*reqs)[0])
	var got string
	if err := json.Unmarshal(msgs[2].Content, &got); err != nil {
		t.Fatalf("tool content not a JSON string: %s", msgs[2].Content)
	}
	if got != `{"a":1}` {
		t.Errorf("tool content = %q, want verbatim %q", got, `{"a":1}`)
	}
}

func TestPlainToolResultKeepsLegacyEncoding(t *testing.T) {
	srv, reqs := recordServer(t, "application/json", okChat)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
	if _, err := model.Generate(context.Background(), provider.Call{Messages: toolHistory("x")}); err != nil {
		t.Fatal(err)
	}
	var got string
	_ = json.Unmarshal(sentMessages(t, (*reqs)[0])[2].Content, &got)
	if got != `"x"` {
		t.Errorf("tool content = %q, want legacy JSON-encoded %q", got, `"x"`)
	}
}

func TestNullAssistantContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   bool
		want string
	}{{"default omits", false, ""}, {"option sends null", true, "null"}} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reqs := recordServer(t, "application/json", okChat)
			model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL, NullAssistantContent: tc.on}, "m")
			if _, err := model.Generate(context.Background(), provider.Call{Messages: toolHistory(provider.RawContent("r"))}); err != nil {
				t.Fatal(err)
			}
			if got := string(sentMessages(t, (*reqs)[0])[1].Content); got != tc.want {
				t.Errorf("assistant content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProviderOptionsPreserveNestedOrderAndNumbers(t *testing.T) {
	srv, reqs := recordServer(t, "application/json", okChat)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
	schema := json.RawMessage(`{"type":"object","properties":{"z":{"type":"integer"},"a":{"type":"integer"}}}`)
	if _, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
		Tools:    []provider.ToolDef{{Name: "f", Schema: schema}},
		ProviderOptions: map[string]any{"t": map[string]any{
			"extra": json.RawMessage(`{"z":1,"a":12345678901234567890}`),
		}},
	}); err != nil {
		t.Fatal(err)
	}
	body := string((*reqs)[0])
	for _, want := range []string{
		`"properties":{"z":{"type":"integer"},"a":{"type":"integer"}}`,
		`"extra":{"z":1,"a":12345678901234567890}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s:\n%s", want, body)
		}
	}
}

func TestResponseHasUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		has        bool
		total      int
	}{
		{"absent", okChat, false, 0},
		{"null", `{"choices":[],"usage":null}`, false, 0},
		{"zero", `{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true, 0},
		{"reported", `{"choices":[],"usage":{"total_tokens":7}}`, true, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := recordServer(t, "application/json", tc.body)
			model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
			resp, err := model.Generate(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
			if err != nil {
				t.Fatal(err)
			}
			if resp.HasUsage != tc.has || resp.Usage.TotalTokens != tc.total {
				t.Errorf("HasUsage=%v total=%d, want %v %d", resp.HasUsage, resp.Usage.TotalTokens, tc.has, tc.total)
			}
		})
	}
}

func streamFinish(t *testing.T, parts []provider.StreamPart) provider.FinishPart {
	t.Helper()
	for _, p := range parts {
		if f, ok := p.(provider.FinishPart); ok {
			return f
		}
	}
	t.Fatal("no FinishPart")
	return provider.FinishPart{}
}

func TestStreamHasUsage(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		has        bool
	}{
		{"absent", "data: {\"choices\":[{\"delta\":{\"content\":\"a\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", false},
		{"present", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":0}}\n\ndata: [DONE]\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := recordServer(t, "text/event-stream", tc.body)
			model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
			s, err := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			f := streamFinish(t, drain(t, s))
			if f.HasUsage != tc.has {
				t.Errorf("HasUsage = %v, want %v", f.HasUsage, tc.has)
			}
		})
	}
}

const toolDeltas = "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"b\",\"function\":{\"name\":\"second\",\"arguments\":\"{\\\"y\\\":2}\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"first\",\"arguments\":\"{}\"}}]}}]}\n\n"

func toolEnds(parts []provider.StreamPart) []string {
	var ids []string
	for _, p := range parts {
		if e, ok := p.(provider.ToolCallEnd); ok {
			ids = append(ids, e.Call.ID)
		}
	}
	return ids
}

func TestStreamToolCallsOrderedByIndex(t *testing.T) {
	body := toolDeltas + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
	srv, _ := recordServer(t, "text/event-stream", body)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
	s, _ := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
	defer func() { _ = s.Close() }()
	if got := strings.Join(toolEnds(drain(t, s)), ","); got != "a,b" {
		t.Errorf("ToolCallEnd order = %s, want a,b (by index)", got)
	}
}

func TestStreamDoneWithoutFinishReasonStillEmitsToolCallEnds(t *testing.T) {
	srv, _ := recordServer(t, "text/event-stream", toolDeltas+"data: [DONE]\n\n")
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
	s, _ := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
	defer func() { _ = s.Close() }()
	parts := drain(t, s)
	if got := strings.Join(toolEnds(parts), ","); got != "a,b" {
		t.Errorf("ToolCallEnd = %s, want a,b", got)
	}
	if f := streamFinish(t, parts); f.Reason != provider.FinishOther {
		t.Errorf("reason = %v", f.Reason)
	}
}

func TestStreamStrictByDefault(t *testing.T) {
	for name, body := range map[string]string{
		"non-json line":  "data: hello\n\ndata: [DONE]\n\n",
		"eof w/o finish": "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := recordServer(t, "text/event-stream", body)
			model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL}, "m")
			s, _ := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
			defer func() { _ = s.Close() }()
			drain(t, s)
			if s.Err() == nil {
				t.Error("want error in strict mode")
			}
		})
	}
}

func TestLenientStreamSkipsNonJSONLines(t *testing.T) {
	body := ": keep-alive\n\ndata: hello\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	srv, _ := recordServer(t, "text/event-stream", body)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL, LenientStream: true}, "m")
	s, _ := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
	defer func() { _ = s.Close() }()
	parts := drain(t, s)
	if s.Err() != nil {
		t.Fatalf("Err = %v", s.Err())
	}
	if f := streamFinish(t, parts); f.Reason != provider.FinishStop {
		t.Errorf("reason = %v", f.Reason)
	}
}

func TestLenientStreamEndsWithoutFinishReason(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n" + toolDeltas
	srv, _ := recordServer(t, "text/event-stream", body)
	model := NewLanguageModel(Config{Name: "t", BaseURL: srv.URL, LenientStream: true}, "m")
	s, _ := model.Stream(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("x")}})
	defer func() { _ = s.Close() }()
	parts := drain(t, s)
	if s.Err() != nil {
		t.Fatalf("Err = %v", s.Err())
	}
	if got := strings.Join(toolEnds(parts), ","); got != "a,b" {
		t.Errorf("ToolCallEnd = %s, want a,b", got)
	}
	if f := streamFinish(t, parts); f.Reason != provider.FinishOther {
		t.Errorf("reason = %v, want other", f.Reason)
	}
}

func TestEmbedWithoutIndexUsesArrayOrder(t *testing.T) {
	srv, _ := recordServer(t, "application/json", `{"data":[{"embedding":[1]},{"embedding":[2]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`)
	model := NewEmbeddingModel(Config{Name: "t", BaseURL: srv.URL}, "m")
	resp, err := model.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 2 || resp.Embeddings[0][0] != 1 || resp.Embeddings[1][0] != 2 {
		t.Errorf("embeddings = %v", resp.Embeddings)
	}
}
