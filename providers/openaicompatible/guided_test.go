package openaicompatible

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func guidedBody(t *testing.T, p *Provider, g Guided) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"positive"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	p.baseURL = srv.URL
	opts, err := p.GuidedOptions(g)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Chat("m").Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ProviderOptions: opts})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestGuidedOptionsStructuredOutputs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"}}}`)
	for name, tc := range map[string]struct {
		g    Guided
		key  string
		want string
	}{
		"json":   {Guided{JSON: schema}, "json", string(schema)},
		"choice": {Guided{Choice: []string{"positive", "negative"}}, "choice", `["positive","negative"]`},
		"regex":  {Guided{Regex: `\w+@\w+\.com`}, "regex", `"\\w+@\\w+\\.com"`},
	} {
		t.Run(name, func(t *testing.T) {
			body := guidedBody(t, New("", WithName("vllm")), tc.g)
			so, _ := body["structured_outputs"].(map[string]any)
			got, _ := json.Marshal(so[tc.key])
			var want any
			_ = json.Unmarshal([]byte(tc.want), &want)
			wantB, _ := json.Marshal(want)
			if len(so) != 1 || string(got) != string(wantB) {
				t.Errorf("structured_outputs = %v, want %s under %q", so, wantB, tc.key)
			}
			for k := range body {
				if strings.HasPrefix(k, "guided_") {
					t.Errorf("legacy field %q sent", k)
				}
			}
		})
	}
}

func TestGuidedOptionsLegacyFields(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	body := guidedBody(t, New("", WithName("vllm")), Guided{JSON: schema, Legacy: true})
	if m, _ := body["guided_json"].(map[string]any); m["type"] != "object" {
		t.Errorf("guided_json = %v", body["guided_json"])
	}
	body = guidedBody(t, New("", WithName("vllm")), Guided{Choice: []string{"a", "b"}, Legacy: true})
	if c, _ := body["guided_choice"].([]any); len(c) != 2 || c[1] != "b" {
		t.Errorf("guided_choice = %v", body["guided_choice"])
	}
	body = guidedBody(t, New("", WithName("vllm")), Guided{Regex: "a+", Legacy: true})
	if body["guided_regex"] != "a+" {
		t.Errorf("guided_regex = %v", body["guided_regex"])
	}
	if _, has := body["structured_outputs"]; has {
		t.Error("structured_outputs sent in legacy mode")
	}
}

func TestGuidedOptionsKeyedByProviderName(t *testing.T) {
	opts, err := New("x", WithName("my-vllm")).GuidedOptions(Guided{Regex: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opts["my-vllm"].(map[string]any); !ok || len(opts) != 1 {
		t.Errorf("opts = %v", opts)
	}
}

func TestGuidedOptionsValidation(t *testing.T) {
	p := New("x")
	for name, g := range map[string]Guided{
		"none":         {},
		"two":          {Regex: "a", Choice: []string{"b"}},
		"three":        {Regex: "a", Choice: []string{"b"}, JSON: json.RawMessage(`{}`)},
		"invalid json": {JSON: json.RawMessage(`{`)},
	} {
		if _, err := p.GuidedOptions(g); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}
