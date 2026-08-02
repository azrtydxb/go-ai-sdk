package google

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat/compattest"
)

func TestEmbeddingModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("text-embedding-test")

	if got := model.ModelID(); got != "text-embedding-test" {
		t.Errorf("ModelID() = %q, want %q", got, "text-embedding-test")
	}
	if got := model.ProviderName(); got != "google" {
		t.Errorf("ProviderName() = %q, want %q", got, "google")
	}
	if got := model.MaxBatchSize(); got != 100 {
		t.Errorf("MaxBatchSize() = %d, want 100", got)
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

	// batchEmbedContents does not return usage information.
	if resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 || resp.Usage.TotalTokens != 0 {
		t.Errorf("Usage = %+v, want zero value", resp.Usage)
	}
}
