package azure

import (
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "azure")
	defer srv.Close()
	providertest.Run(t, providertest.Config{
		Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-deployment"),
		ProviderName: "azure",
	})
}

func TestDefaults(t *testing.T) {
	p := New(WithAPIKey("k"), WithResourceName("myres"))
	want := "https://myres.openai.azure.com/openai/v1"
	if got := p.resolvedBaseURL(); got != want {
		t.Fatalf("resolvedBaseURL() = %q, want %q", got, want)
	}
	m := p.Model("my-deployment")
	if m.ProviderName() != "azure" {
		t.Fatalf("ProviderName = %q", m.ProviderName())
	}
	if !m.Capabilities().NativeJSON {
		t.Fatal("NativeJSON should be true")
	}
}

func TestBaseURLOverridesResourceName(t *testing.T) {
	p := New(WithAPIKey("k"), WithResourceName("myres"), WithBaseURL("https://custom.example.com/openai/v1"))
	if got := p.resolvedBaseURL(); got != "https://custom.example.com/openai/v1" {
		t.Fatalf("resolvedBaseURL() = %q, want override", got)
	}
}

func TestAPIKeyHeaderSent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "azure")
	defer srv.Close()
	m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("test-deployment")
	if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}}); err != nil {
		t.Fatal(err)
	}

	if got := srv.HeaderValues("api-key"); len(got) != 1 || got[0] != "secret-k" {
		t.Fatalf("api-key header = %v, want [secret-k]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "" {
		t.Fatalf("Authorization header = %v, want no Authorization header", got)
	}
}

func TestEmbeddings(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "azure")
	defer srv.Close()
	em := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-deployment")
	if em.MaxBatchSize() != 2048 {
		t.Fatalf("MaxBatchSize = %d, want 2048", em.MaxBatchSize())
	}
	resp, err := em.Embed(t.Context(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Embeddings) != 3 {
		t.Fatalf("embeddings = %d", len(resp.Embeddings))
	}
}

func TestNoBaseURLErrors(t *testing.T) {
	p := New(WithAPIKey("k"))
	p.resourceName = "" // ensure no env leakage from AZURE_RESOURCE_NAME

	m := p.Model("test-deployment")
	_, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}})
	if err == nil {
		t.Fatal("Generate: want error when neither resource name nor base URL configured, got nil")
	}
	if !strings.Contains(err.Error(), "base URL not configured") {
		t.Errorf("error = %q, want it to mention base URL not configured", err.Error())
	}

	em := p.EmbeddingModel("embed-deployment")
	_, err = em.Embed(t.Context(), []string{"a"})
	if err == nil {
		t.Fatal("Embed: want error when neither resource name nor base URL configured, got nil")
	}
	if !strings.Contains(err.Error(), "base URL not configured") {
		t.Errorf("error = %q, want it to mention base URL not configured", err.Error())
	}
}
