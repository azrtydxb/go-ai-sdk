package deepseek

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "deepseek")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-model"),
		ProviderName: "deepseek",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"))
	if p.baseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	m := p.Model("m")
	if m.ProviderName() != "deepseek" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if !m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be true")
	}
}

func TestAuthHeaderAndModelSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "deepseek")
	defer srv.Close()
	m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("deepseek-chat")
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}}); err != nil {
		t.Fatal(err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(reqs[0], &body); err != nil || body.Model != "deepseek-chat" {
		t.Fatalf("model in request = %q err=%v", body.Model, err)
	}
	if srv.AuthHeaders()[0] != "Bearer secret-k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}

func TestMaxTokensUsesMaxTokensField(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "deepseek")
	defer srv.Close()
	m := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("test-model")

	maxTokens := 42
	if _, err := m.Generate(t.Context(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		MaxTokens: &maxTokens,
	}); err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(srv.Requests()[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var n int
	if err := json.Unmarshal(raw["max_tokens"], &n); err != nil || n != 42 {
		t.Errorf("max_tokens = %s, want 42", raw["max_tokens"])
	}
	if _, ok := raw["max_completion_tokens"]; ok {
		t.Errorf("request unexpectedly contains max_completion_tokens: %s", srv.Requests()[0])
	}
}

// TestResponseFormatJSONObjectOnly asserts that DeepSeek's preset always
// serializes response_format as {"type":"json_object"}, dropping any Schema
// — DeepSeek rejects json_schema response_format.
func TestResponseFormatJSONObjectOnly(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "deepseek")
	defer srv.Close()
	m := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("test-model")

	if _, err := m.Generate(t.Context(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		ResponseFormat: &provider.ResponseFormat{
			Type:   "json",
			Name:   "weather_schema",
			Schema: json.RawMessage(`{"type":"object"}`),
		},
	}); err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(srv.Requests()[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	rfRaw, ok := raw["response_format"]
	if !ok {
		t.Fatalf("request missing response_format field: %s", srv.Requests()[0])
	}
	if strings.Contains(string(rfRaw), "json_schema") || strings.Contains(string(rfRaw), "schema") {
		t.Errorf("response_format = %s, want no json_schema/schema keys", rfRaw)
	}
	var rf map[string]string
	if err := json.Unmarshal(rfRaw, &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %q, want json_object", rf["type"])
	}
}
