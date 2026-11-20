package geminicompat

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func newGeminiCapture(t *testing.T, sse bool, reply string) (provider.LanguageModel, *map[string]any) {
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
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return NewLanguageModel(testConfig(srv.URL), "gemini-test"), &body
}

const geminiLogprobsReply = `{"candidates":[{"content":{"parts":[{"text":"Hi"}]},"finishReason":"STOP",
 "logprobsResult":{
  "topCandidates":[{"candidates":[{"token":"Hi","tokenId":1,"logProbability":-0.25},{"token":"Hello","tokenId":2,"logProbability":-1.5}]}],
  "chosenCandidates":[{"token":"Hi","tokenId":1,"logProbability":-0.25}]}}]}`

func TestGeminiLogprobsRequestAndResponse(t *testing.T) {
	m, body := newGeminiCapture(t, false, geminiLogprobsReply)
	top := 2
	resp, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true, TopLogprobs: &top})
	if err != nil {
		t.Fatal(err)
	}
	gc, _ := (*body)["generationConfig"].(map[string]any)
	if gc["responseLogprobs"] != true || gc["logprobs"] != float64(2) {
		t.Errorf("generationConfig = %v", gc)
	}
	if len(resp.Logprobs) != 1 {
		t.Fatalf("Logprobs = %+v", resp.Logprobs)
	}
	lp := resp.Logprobs[0]
	if lp.Token != "Hi" || lp.Logprob != -0.25 || len(lp.TopLogprobs) != 2 || lp.TopLogprobs[1].Token != "Hello" || lp.TopLogprobs[1].Logprob != -1.5 {
		t.Errorf("logprob = %+v", lp)
	}
}

func TestGeminiLogprobsOnlyWhenRequested(t *testing.T) {
	m, body := newGeminiCapture(t, false, `{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"STOP"}]}`)
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*body)["generationConfig"]; ok {
		t.Errorf("generationConfig sent unprompted: %v", (*body)["generationConfig"])
	}
}

func TestGeminiTopLogprobsRequiresLogprobs(t *testing.T) {
	m, body := newGeminiCapture(t, false, geminiLogprobsReply)
	top := 2
	_, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, TopLogprobs: &top})
	if err == nil || !strings.Contains(err.Error(), "requires Call.Logprobs") {
		t.Fatalf("err = %v", err)
	}
	if *body != nil {
		t.Error("request reached the server")
	}
}

func TestGeminiLogprobsStreaming(t *testing.T) {
	sse := "data: " + strings.Join(strings.Fields(geminiLogprobsReply), "") + "\n\n"
	m, _ := newGeminiCapture(t, true, sse)
	st, err := m.Stream(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var deltas []provider.LogprobsDelta
	for p := range st.Parts() {
		if d, ok := p.(provider.LogprobsDelta); ok {
			deltas = append(deltas, d)
		}
	}
	if st.Err() != nil {
		t.Fatal(st.Err())
	}
	if len(deltas) != 1 || len(deltas[0].Tokens) != 1 || deltas[0].Tokens[0].Token != "Hi" {
		t.Fatalf("deltas = %+v", deltas)
	}
}

func TestGeminiStrictFalseUnsupported(t *testing.T) {
	m, body := newGeminiCapture(t, false, geminiLogprobsReply)
	no := false
	_, err := m.Generate(t.Context(), provider.Call{
		Messages:       []provider.Message{provider.UserText("hi")},
		ResponseFormat: &provider.ResponseFormat{Type: "json", Schema: json.RawMessage(`{"type":"object"}`), Strict: &no},
	})
	var ufe *provider.UnsupportedFeatureError
	if !errors.As(err, &ufe) || ufe.Provider != "google" || !strings.Contains(ufe.Feature, "Strict") {
		t.Fatalf("err = %v", err)
	}
	if *body != nil {
		t.Error("request reached the server")
	}
}
