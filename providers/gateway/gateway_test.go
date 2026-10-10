package gateway

import (
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "gateway")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("openai/gpt-4o"),
		ProviderName: "gateway",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"))
	if p.baseURL != "https://ai-gateway.vercel.sh/v1" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	m := p.Model("openai/gpt-4o")
	if m.ProviderName() != "gateway" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be false")
	}
}

func TestAuthHeaderAndModelSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "gateway")
	defer srv.Close()
	m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("anthropic/claude-3-5-sonnet")
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
	if err := json.Unmarshal(reqs[0], &body); err != nil || body.Model != "anthropic/claude-3-5-sonnet" {
		t.Fatalf("model in request = %q err=%v", body.Model, err)
	}
	if srv.AuthHeaders()[0] != "Bearer secret-k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}

func TestEmbeddingRequestShape(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "gateway")
	defer srv.Close()
	em := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("openai/text-embedding-3-small")
	if em.MaxBatchSize() != 1 {
		t.Fatalf("MaxBatchSize = %d", em.MaxBatchSize())
	}
	resp, err := em.Embed(t.Context(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 3 {
		t.Fatalf("embeddings = %d", len(resp.Embeddings))
	}

	var body struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	if err := json.Unmarshal(srv.Requests()[0], &body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if body.Model != "openai/text-embedding-3-small" {
		t.Fatalf("model = %q", body.Model)
	}
	if len(body.Input) != 3 {
		t.Fatalf("input = %v", body.Input)
	}
	if srv.AuthHeaders()[0] != "Bearer k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}

// TestResponseFormatJSONSchemaPassthrough asserts the Gateway's distinctive
// wire knob: its conservative NativeJSON:false capability does NOT degrade
// the wire request. NativeJSON only advertises capability; the Gateway does
// not set JSONObjectOnly, so an explicit JSON ResponseFormat carrying a
// schema is still sent verbatim as
// {"type":"json_schema","json_schema":{...,"strict":true}} — JSON-mode
// support varies by the upstream model the routing slug resolves to, but a
// caller who explicitly asks for schema-enforced output still gets one.
func TestResponseFormatJSONSchemaPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "gateway")
	defer srv.Close()
	m := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("openai/gpt-4o")

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

// TestRegistryRoundTrip proves that ai.Registry's splitID (first-colon split)
// handles a slash-bearing gateway routing slug correctly: "gateway:openai/gpt-4o"
// splits into provider "gateway" and model "openai/gpt-4o", not truncated at
// the slash.
func TestRegistryRoundTrip(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "gateway")
	defer srv.Close()

	r := ai.NewRegistry()
	r.Register("gateway", New(WithAPIKey("k"), WithBaseURL(srv.URL)))

	m, err := r.LanguageModel("gateway:openai/gpt-4o")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}}); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(srv.Requests()[0], &body); err != nil || body.Model != "openai/gpt-4o" {
		t.Fatalf("model in request = %q err=%v", body.Model, err)
	}
}
