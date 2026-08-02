package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

	model := NewEmbeddingModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "test-embed")
	_, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("Embed: want error for missing index, got nil")
	}
	if !strings.Contains(err.Error(), "index 1") {
		t.Errorf("error = %q, want it to mention missing index 1", err.Error())
	}
}
