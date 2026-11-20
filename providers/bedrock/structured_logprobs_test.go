package bedrock

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

func newStructuredServer(t *testing.T) (provider.LanguageModel, *map[string]any) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = nil
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"output":{"message":{"role":"assistant","content":[{"text":"{\"label\":\"ok\"}"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`)
	}))
	t.Cleanup(srv.Close)
	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", ""),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)
	return p.Model("anthropic.claude-3-sonnet-20240229-v1:0"), &body
}

func userCall(rf *provider.ResponseFormat) provider.Call {
	return provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ResponseFormat: rf}
}

func TestResponseFormatSchemaMapsToTextFormat(t *testing.T) {
	m, body := newStructuredServer(t)
	schema := json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}}}`)
	resp, err := m.Generate(t.Context(), userCall(&provider.ResponseFormat{Type: "json", Schema: schema, Name: "verdict"}))
	if err != nil {
		t.Fatal(err)
	}
	oc, _ := (*body)["outputConfig"].(map[string]any)
	tf, _ := oc["textFormat"].(map[string]any)
	structure, _ := tf["structure"].(map[string]any)
	js, _ := structure["jsonSchema"].(map[string]any)
	if tf["type"] != "json_schema" || js["name"] != "verdict" {
		t.Fatalf("outputConfig = %v", oc)
	}
	// Converse takes the schema as a string of JSON text.
	str, ok := js["schema"].(string)
	if !ok {
		t.Fatalf("schema is %T, want string", js["schema"])
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(str), &parsed); err != nil || parsed["type"] != "object" {
		t.Errorf("schema string = %q (%v)", str, err)
	}
	if resp.Text() != `{"label":"ok"}` {
		t.Errorf("text = %q", resp.Text())
	}
}

func TestResponseFormatTextOmitsOutputConfig(t *testing.T) {
	m, body := newStructuredServer(t)
	if _, err := m.Generate(t.Context(), userCall(&provider.ResponseFormat{Type: "text"})); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*body)["outputConfig"]; ok {
		t.Errorf("outputConfig sent for text: %v", (*body)["outputConfig"])
	}
}

func TestResponseFormatUnsupportedCases(t *testing.T) {
	no := false
	for name, tc := range map[string]struct {
		call provider.Call
		want string
	}{
		"json_object":  {userCall(&provider.ResponseFormat{Type: "json"}), "json_object"},
		"strict false": {userCall(&provider.ResponseFormat{Type: "json", Schema: json.RawMessage(`{}`), Strict: &no}), "Strict"},
		"logprobs":     {provider.Call{Messages: []provider.Message{provider.UserText("hi")}, Logprobs: true}, "Logprobs"},
	} {
		t.Run(name, func(t *testing.T) {
			m, body := newStructuredServer(t)
			_, err := m.Generate(t.Context(), tc.call)
			var ufe *provider.UnsupportedFeatureError
			if !errors.As(err, &ufe) || ufe.Provider != "bedrock" || !strings.Contains(ufe.Feature, tc.want) {
				t.Fatalf("err = %v", err)
			}
			if *body != nil {
				t.Error("request reached the server")
			}
			if _, err := m.Stream(t.Context(), tc.call); !errors.As(err, &ufe) {
				t.Errorf("Stream err = %v, want UnsupportedFeatureError", err)
			}
		})
	}
}
