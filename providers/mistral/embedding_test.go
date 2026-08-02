package mistral

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func newEmbeddingFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")

		// Respond with entries out of order (index 2, 0, 1) to exercise
		// re-indexing by the wire's "index" field rather than array
		// position.
		resp := embeddingResponse{
			Data: []embeddingData{
				{Index: 2, Embedding: []float64{0.3, 0.3}},
				{Index: 0, Embedding: []float64{0.1, 0.1}},
				{Index: 1, Embedding: []float64{0.2, 0.2}},
			},
			Usage: wireUsage{PromptTokens: 9, TotalTokens: 9},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEmbedOutOfOrderIndexReindexed(t *testing.T) {
	srv := newEmbeddingFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("mistral-embed")

	resp, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(resp.Embeddings) != 3 {
		t.Fatalf("Embeddings = %d, want 3", len(resp.Embeddings))
	}
	if resp.Embeddings[0][0] != 0.1 {
		t.Errorf("Embeddings[0] = %v, want first value 0.1", resp.Embeddings[0])
	}
	if resp.Embeddings[1][0] != 0.2 {
		t.Errorf("Embeddings[1] = %v, want first value 0.2", resp.Embeddings[1])
	}
	if resp.Embeddings[2][0] != 0.3 {
		t.Errorf("Embeddings[2] = %v, want first value 0.3", resp.Embeddings[2])
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.TotalTokens != 9 {
		t.Errorf("Usage = %+v, want InputTokens=9 TotalTokens=9", resp.Usage)
	}
}

func TestEmbedModelMetadata(t *testing.T) {
	srv := newEmbeddingFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("mistral-embed")

	if got := model.ModelID(); got != "mistral-embed" {
		t.Errorf("ModelID() = %q, want mistral-embed", got)
	}
	if got := model.ProviderName(); got != "mistral" {
		t.Errorf("ProviderName() = %q, want mistral", got)
	}
	if got := model.MaxBatchSize(); got != 32 {
		t.Errorf("MaxBatchSize() = %d, want 32", got)
	}
}

func TestEmbedMissingIndexErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Response has 3 entries (matching len(values)) but duplicates
		// index 0 and omits index 1 entirely, leaving a nil slot.
		resp := embeddingResponse{
			Data: []embeddingData{
				{Index: 0, Embedding: []float64{0.1, 0.1}},
				{Index: 0, Embedding: []float64{0.1, 0.1}},
				{Index: 2, Embedding: []float64{0.3, 0.3}},
			},
			Usage: wireUsage{PromptTokens: 9, TotalTokens: 9},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("mistral-embed")
	_, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("Embed: want error for missing index, got nil")
	}
	if !strings.Contains(err.Error(), "index 1") {
		t.Errorf("error = %q, want it to mention missing index 1", err.Error())
	}
}

func TestEmbedErrorPropagatesAPICallError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"message":"bad request"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("mistral-embed")
	_, err := model.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed: want error, got nil")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *ai.APICallError (via errors.As)", err, err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
