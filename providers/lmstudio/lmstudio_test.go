package lmstudio

import (
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "lmstudio")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-model"),
		ProviderName: "lmstudio",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"))
	if p.baseURL != "http://localhost:1234/v1" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	m := p.Model("m")
	if m.ProviderName() != "lmstudio" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if !m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be true")
	}
}

func TestAuthHeaderAndModelSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "lmstudio")
	defer srv.Close()
	m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("local-model")
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
	if err := json.Unmarshal(reqs[0], &body); err != nil || body.Model != "local-model" {
		t.Fatalf("model in request = %q err=%v", body.Model, err)
	}
	if srv.AuthHeaders()[0] != "Bearer secret-k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}

// TestResponseFormatJSONSchemaPassthrough asserts LM Studio's distinguishing
// wire knob: its NativeJSON:true preset (no JSONObjectOnly) sends a JSON
// response_format with a schema through to the wire verbatim as
// {"type":"json_schema","json_schema":{...,"strict":true}} — the local
// server's structured-output support is treated as native, and callers whose
// loaded model rejects json_schema can drop to json_object via
// ProviderOptions.
func TestResponseFormatJSONSchemaPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "lmstudio")
	defer srv.Close()
	m := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("local-model")

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

	var rf responseFormat
	if err := json.Unmarshal(responseFormatRaw(t, srv), &rf); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if rf.Type != "json_schema" {
		t.Errorf("response_format.type = %q, want json_schema", rf.Type)
	}
	if rf.JSONSchema == nil {
		t.Fatalf("response_format missing json_schema: %s", responseFormatRaw(t, srv))
	}
	if rf.JSONSchema.Name != "weather_schema" {
		t.Errorf("json_schema.name = %q, want weather_schema", rf.JSONSchema.Name)
	}
	if !rf.JSONSchema.Strict {
		t.Error("json_schema.strict = false, want true")
	}
	if string(rf.JSONSchema.Schema) != `{"type":"object"}` {
		t.Errorf("json_schema.schema = %s, want {\"type\":\"object\"}", rf.JSONSchema.Schema)
	}
}

// responseFormat is the wire shape of the OpenAI response_format field.
type responseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
		Strict bool            `json:"strict"`
	} `json:"json_schema"`
}

// responseFormatRaw extracts the raw response_format field from the single
// request the fixture server has recorded.
func responseFormatRaw(t *testing.T, srv *compattest.Server) json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(srv.Requests()[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	rf, ok := raw["response_format"]
	if !ok {
		t.Fatalf("request missing response_format field: %s", srv.Requests()[0])
	}
	return rf
}

// TestEmptyAPIKeyStillWorks verifies LM Studio's local-first default (no API
// key configured) still sends a well-formed request: openaicompat sends
// "Authorization: Bearer " with an empty key, which LM Studio ignores, but
// the request must not fail to build or send.
func TestEmptyAPIKeyStillWorks(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "lmstudio")
	defer srv.Close()
	m := New(WithAPIKey(""), WithBaseURL(srv.URL)).Model("local-model")
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}}); err != nil {
		t.Fatal(err)
	}
	if got := srv.AuthHeaders()[0]; got != "Bearer" && got != "Bearer " {
		t.Fatalf("auth header = %q", got)
	}
}

func TestEmbeddingRequestShape(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "lmstudio")
	defer srv.Close()
	em := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-model")
	if em.MaxBatchSize() != 1 {
		t.Fatalf("MaxBatchSize = %d", em.MaxBatchSize())
	}
	resp, err := em.Embed(t.Context(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 1 {
		t.Fatalf("embeddings = %d", len(resp.Embeddings))
	}

	var body struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.Unmarshal(srv.Requests()[0], &body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if body.Model != "embed-model" {
		t.Fatalf("model = %q", body.Model)
	}
	if len(body.Input) != 1 {
		t.Fatalf("input = %v", body.Input)
	}
}
