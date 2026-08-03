package baseten

import (
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "baseten")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-model"),
		ProviderName: "baseten",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"))
	if p.baseURL != "https://inference.baseten.co/v1" {
		t.Fatalf("baseURL = %q", p.baseURL)
	}
	m := p.Model("m")
	if m.ProviderName() != "baseten" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if !m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be true")
	}
}

func TestAuthHeaderAndModelSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "baseten")
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

func TestEmbeddingRequestShape(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "baseten")
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
	if srv.AuthHeaders()[0] != "Bearer k" {
		t.Fatalf("auth header = %q", srv.AuthHeaders()[0])
	}
}
