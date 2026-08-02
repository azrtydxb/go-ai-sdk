package fireworks

import (
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "fireworks")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-model"),
		ProviderName: "fireworks",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"))
	if p.baseURL != "https://api.fireworks.ai/inference/v1" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	m := p.Model("m")
	if m.ProviderName() != "fireworks" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if !m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be true")
	}
}

func TestAuthHeaderAndModelSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "fireworks")
	defer srv.Close()
	m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("test-model")
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
	if err := json.Unmarshal(reqs[0], &body); err != nil || body.Model != "test-model" {
		t.Fatalf("model in request = %q err=%v", body.Model, err)
	}
	if srv.AuthHeaders()[0] != "Bearer secret-k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}

func TestMaxTokensUsesMaxTokensField(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "fireworks")
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

func TestEmbeddings(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "fireworks")
	defer srv.Close()
	em := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-model")
	if em.MaxBatchSize() != 100 {
		t.Fatalf("MaxBatchSize = %d", em.MaxBatchSize())
	}
	resp, err := em.Embed(t.Context(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 3 {
		t.Fatalf("embeddings = %d", len(resp.Embeddings))
	}
}
