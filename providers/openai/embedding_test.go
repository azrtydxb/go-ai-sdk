package openai

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
)

func TestEmbeddingModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openai")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("text-embedding-test")

	if got := model.ModelID(); got != "text-embedding-test" {
		t.Errorf("ModelID() = %q, want %q", got, "text-embedding-test")
	}
	if got := model.ProviderName(); got != "openai" {
		t.Errorf("ProviderName() = %q, want %q", got, "openai")
	}
	if got := model.MaxBatchSize(); got != 2048 {
		t.Errorf("MaxBatchSize() = %d, want 2048", got)
	}

	values := []string{"a", "bb", "ccc"}
	resp, err := model.Embed(context.Background(), values)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(resp.Embeddings) != 3 {
		t.Fatalf("Embeddings = %d, want 3", len(resp.Embeddings))
	}
	for i, v := range values {
		want := []float64{float64(i), float64(len(v))}
		got := resp.Embeddings[i]
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("Embeddings[%d] = %v, want %v", i, got, want)
		}
	}

	wantTotal := len("a") + len("bb") + len("ccc")
	if resp.Usage.InputTokens != wantTotal {
		t.Errorf("Usage.InputTokens = %d, want %d", resp.Usage.InputTokens, wantTotal)
	}
	if resp.Usage.TotalTokens != wantTotal {
		t.Errorf("Usage.TotalTokens = %d, want %d", resp.Usage.TotalTokens, wantTotal)
	}
}
