package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// newCaptureServer replies with reply to POST /chat/completions (content
// type chosen by sse) and records the last request body.
func newCaptureServer(t *testing.T, status int, sse bool, reply string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(raw, &body)
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

const logprobsReply = `{"choices":[{"message":{"content":"Hi"},"finish_reason":"stop",
 "logprobs":{"content":[
  {"token":"Hi","logprob":-0.25,"bytes":[72,105],"top_logprobs":[
     {"token":"Hi","logprob":-0.25,"bytes":[72,105]},{"token":"Hello","logprob":-1.5,"bytes":[72,101]}]},
  {"token":"!","logprob":-3,"bytes":null,"top_logprobs":[]}]}}],
 "usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`

func TestResponseFormatRequestShapes(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}}}`)
	no := false
	for _, tc := range []struct {
		name string
		rf   provider.ResponseFormat
		cfg  Config
		want string // JSON of the expected response_format
	}{
		{"json_object", provider.ResponseFormat{Type: "json"}, Config{}, `{"type":"json_object"}`},
		{"json_schema defaults strict", provider.ResponseFormat{Type: "json", Schema: schema, Name: "verdict"}, Config{},
			`{"type":"json_schema","json_schema":{"name":"verdict","schema":` + string(schema) + `,"strict":true}}`},
		{"json_schema strict false", provider.ResponseFormat{Type: "json", Schema: schema, Name: "verdict", Strict: &no}, Config{},
			`{"type":"json_schema","json_schema":{"name":"verdict","schema":` + string(schema) + `,"strict":false}}`},
		{"json_schema default name", provider.ResponseFormat{Type: "json", Schema: schema}, Config{},
			`{"type":"json_schema","json_schema":{"name":"response","schema":` + string(schema) + `,"strict":true}}`},
		{"object-only provider drops schema", provider.ResponseFormat{Type: "json", Schema: schema, Name: "v"}, Config{JSONObjectOnly: true},
			`{"type":"json_object"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, body := newCaptureServer(t, 200, false, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`)
			tc.cfg.Name, tc.cfg.BaseURL = "vllm", srv.URL
			m := NewLanguageModel(tc.cfg, "m")
			if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ResponseFormat: &tc.rf}); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal((*body)["response_format"])
			if string(got) != canonical(t, tc.want) {
				t.Errorf("response_format = %s, want %s", got, canonical(t, tc.want))
			}
		})
	}
}

func canonical(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func TestResponseFormatTextOmitted(t *testing.T) {
	srv, body := newCaptureServer(t, 200, false, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	_, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ResponseFormat: &provider.ResponseFormat{Type: "text"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (*body)["response_format"]; ok {
		t.Errorf("response_format sent for text: %v", (*body)["response_format"])
	}
}

func TestLogprobsRequestShape(t *testing.T) {
	srv, body := newCaptureServer(t, 200, false, logprobsReply)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	top := 2
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true, TopLogprobs: &top}); err != nil {
		t.Fatal(err)
	}
	if (*body)["logprobs"] != true || (*body)["top_logprobs"] != float64(2) {
		t.Errorf("body = %v", *body)
	}

	// Not requested: neither field is sent.
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*body)["logprobs"]; ok {
		t.Errorf("logprobs sent when not requested: %v", *body)
	}
	if _, ok := (*body)["top_logprobs"]; ok {
		t.Errorf("top_logprobs sent when not requested: %v", *body)
	}
}

func TestTopLogprobsRequiresLogprobs(t *testing.T) {
	srv, body := newCaptureServer(t, 200, false, logprobsReply)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	top := 3
	for name, run := range map[string]func() error{
		"generate": func() error {
			_, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, TopLogprobs: &top})
			return err
		},
		"stream": func() error {
			_, err := m.Stream(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, TopLogprobs: &top})
			return err
		},
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "requires Call.Logprobs") {
			t.Errorf("%s err = %v", name, err)
		}
	}
	if *body != nil {
		t.Errorf("request reached the server: %v", *body)
	}
}

func TestLogprobsResponseParsing(t *testing.T) {
	srv, _ := newCaptureServer(t, 200, false, logprobsReply)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	top := 2
	resp, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true, TopLogprobs: &top})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Logprobs) != 2 {
		t.Fatalf("Logprobs = %+v", resp.Logprobs)
	}
	first := resp.Logprobs[0]
	if first.Token != "Hi" || first.Logprob != -0.25 || string(first.Bytes) != "Hi" || len(first.TopLogprobs) != 2 {
		t.Errorf("first = %+v", first)
	}
	if alt := first.TopLogprobs[1]; alt.Token != "Hello" || alt.Logprob != -1.5 || string(alt.Bytes) != "He" {
		t.Errorf("alt = %+v", alt)
	}
	if second := resp.Logprobs[1]; second.Token != "!" || second.Bytes != nil || len(second.TopLogprobs) != 0 {
		t.Errorf("second = %+v", second)
	}
}

func TestLogprobsAbsentWhenServerSendsNone(t *testing.T) {
	srv, _ := newCaptureServer(t, 200, false, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop","logprobs":null}]}`)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	resp, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Logprobs != nil {
		t.Errorf("Logprobs = %+v, want nil", resp.Logprobs)
	}
}

func TestLogprobsStreaming(t *testing.T) {
	sse := "data: " + `{"choices":[{"delta":{"content":"Hi"},"logprobs":{"content":[{"token":"Hi","logprob":-0.25,"bytes":[72,105],"top_logprobs":[{"token":"Hello","logprob":-1.5,"bytes":null}]}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{"content":"!"},"logprobs":{"content":[{"token":"!","logprob":-3,"bytes":[33],"top_logprobs":[]}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"delta":{},"finish_reason":"stop","logprobs":null}]}` + "\n\n" +
		"data: [DONE]\n\n"
	srv, body := newCaptureServer(t, 200, true, sse)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	top := 1
	st, err := m.Stream(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true, TopLogprobs: &top})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var deltas []provider.LogprobsDelta
	var text string
	for p := range st.Parts() {
		switch v := p.(type) {
		case provider.LogprobsDelta:
			deltas = append(deltas, v)
		case provider.TextDelta:
			text += v.Text
		}
	}
	if st.Err() != nil {
		t.Fatal(st.Err())
	}
	if (*body)["logprobs"] != true || (*body)["top_logprobs"] != float64(1) || (*body)["stream"] != true {
		t.Errorf("body = %v", *body)
	}
	if text != "Hi!" || len(deltas) != 2 {
		t.Fatalf("text=%q deltas=%+v", text, deltas)
	}
	if d := deltas[0].Tokens[0]; d.Token != "Hi" || d.Logprob != -0.25 || len(d.TopLogprobs) != 1 || d.TopLogprobs[0].Token != "Hello" {
		t.Errorf("delta0 = %+v", d)
	}
	if d := deltas[1].Tokens[0]; d.Token != "!" || string(d.Bytes) != "!" {
		t.Errorf("delta1 = %+v", d)
	}
}

func TestStructuredOutputErrorIsAPICallError(t *testing.T) {
	srv, _ := newCaptureServer(t, 400, false, `{"error":{"message":"json_schema is not supported by this model"}}`)
	m := NewLanguageModel(Config{Name: "vllm", BaseURL: srv.URL}, "m")
	_, err := m.Generate(context.Background(), provider.Call{
		Messages:       []provider.Message{provider.UserText("hi")},
		ResponseFormat: &provider.ResponseFormat{Type: "json", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 || !strings.Contains(apiErr.Message, "not supported") {
		t.Fatalf("err = %v", err)
	}
}
