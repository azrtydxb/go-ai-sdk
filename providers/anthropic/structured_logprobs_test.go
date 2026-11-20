package anthropic

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
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"label\":\"ok\"}"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	return New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test"), &body
}

func userCall(rf *provider.ResponseFormat) provider.Call {
	return provider.Call{Messages: []provider.Message{provider.UserText("hi")}, ResponseFormat: rf}
}

func TestResponseFormatSchemaMapsToOutputConfig(t *testing.T) {
	m, body := newStructuredServer(t)
	schema := json.RawMessage(`{"type":"object","properties":{"label":{"type":"string"}},"required":["label"],"additionalProperties":false}`)
	resp, err := m.Generate(t.Context(), userCall(&provider.ResponseFormat{Type: "json", Schema: schema, Name: "verdict"}))
	if err != nil {
		t.Fatal(err)
	}
	oc, _ := (*body)["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config = %v", oc)
	}
	got, _ := json.Marshal(format["schema"])
	var want any
	_ = json.Unmarshal(schema, &want)
	wantB, _ := json.Marshal(want)
	if string(got) != string(wantB) {
		t.Errorf("schema = %s, want %s", got, wantB)
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
	if _, ok := (*body)["output_config"]; ok {
		t.Errorf("output_config sent for text: %v", (*body)["output_config"])
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
			if !errors.As(err, &ufe) || ufe.Provider != "anthropic" || !strings.Contains(ufe.Feature, tc.want) {
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

func TestCapabilitiesNoJSONObject(t *testing.T) {
	m, _ := newStructuredServer(t)
	if !m.Capabilities().NoJSONObject {
		t.Error("NoJSONObject = false, want true")
	}
}
