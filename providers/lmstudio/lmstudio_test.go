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
