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
