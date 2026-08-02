package geminicompat

// White-box (package geminicompat) test for a defensive check in Embed:
// batchEmbedContents replies with one embedding per request, in order, and
// the response carries no per-embedding identifier to correlate it back to
// its input value — so if the response's embeddings array is shorter than
// the number of values requested, silently zipping them together would
// return wrong (mis-paired) embeddings for a subset of the input rather
// than failing loudly. Embed must instead error when the counts disagree.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbed_ShortResponseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two values requested, only one embedding returned.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"embeddings":[{"values":[0.1,0.2]}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
		EmbedBatch:  100,
	}
	model := NewEmbeddingModel(cfg, "text-embedding-test")

	_, err := model.Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("Embed: want error when response has fewer embeddings than requested values, got nil")
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "1") {
		t.Errorf("error = %q, want it to mention both counts (requested 2, got 1)", err.Error())
	}
}
