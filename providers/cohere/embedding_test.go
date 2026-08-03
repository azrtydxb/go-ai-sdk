package cohere

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func newEmbeddingFixtureServer(t *testing.T, capture *embeddingRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}
		if capture != nil {
			*capture = req
		}

		w.Header().Set("Content-Type", "application/json")
		resp := embeddingResponse{
			Embeddings: embeddingsWire{Float: [][]float64{
				{0.1, 0.1},
				{0.2, 0.2},
				{0.3, 0.3},
			}},
			Meta: embeddingMeta{BilledUnits: embeddingBilledUnits{InputTokens: 9}},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEmbedRequestShape(t *testing.T) {
	var captured embeddingRequest
	srv := newEmbeddingFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-test")

	_, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if captured.InputType != "search_document" {
		t.Errorf("input_type = %q, want search_document", captured.InputType)
	}
	if len(captured.EmbeddingTypes) != 1 || captured.EmbeddingTypes[0] != "float" {
		t.Errorf("embedding_types = %v, want [float]", captured.EmbeddingTypes)
	}
	if len(captured.Texts) != 3 {
		t.Errorf("texts = %v, want 3 entries", captured.Texts)
	}
}

func TestEmbedResultFloatVectorsAndUsage(t *testing.T) {
	srv := newEmbeddingFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-test")

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
		t.Errorf("Usage = %+v, want InputTokens=9 TotalTokens=9 (from billed_units)", resp.Usage)
	}
}

// TestEmbedResponseCountMismatchErrors covers a short/mismatched embed
// response: cohere's /embed endpoint returns embeddings positionally with
// no per-embedding identifier to correlate them back to their input text,
// so a response with fewer embeddings than requested texts would otherwise
// silently mis-zip downstream (e.g. embedding[i] no longer corresponds to
// values[i]). Embed must fail loudly instead.
func TestEmbedResponseCountMismatchErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := embeddingResponse{
			Embeddings: embeddingsWire{Float: [][]float64{
				{0.1, 0.1},
				{0.2, 0.2},
			}},
			Meta: embeddingMeta{BilledUnits: embeddingBilledUnits{InputTokens: 9}},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-test")
	_, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("Embed: want error for a 2-embedding response to 3 inputs, got nil")
	}
}

func TestEmbedModelMetadata(t *testing.T) {
	srv := newEmbeddingFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-test")

	if got := model.ModelID(); got != "embed-test" {
		t.Errorf("ModelID() = %q, want embed-test", got)
	}
	if got := model.ProviderName(); got != "cohere" {
		t.Errorf("ProviderName() = %q, want cohere", got)
	}
	if got := model.MaxBatchSize(); got != 96 {
		t.Errorf("MaxBatchSize() = %d, want 96", got)
	}
}

func TestEmbedErrorPropagatesAPICallError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"message":"bad request"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-test")
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
