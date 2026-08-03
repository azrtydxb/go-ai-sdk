package voyage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// capturedEmbeddingRequest holds both the typed decode of a captured
// request body (for asserting field values) and the raw JSON object (for
// asserting presence/absence of optional fields like input_type).
type capturedEmbeddingRequest struct {
	embeddingRequest
	raw map[string]json.RawMessage
}

func newEmbeddingFixtureServer(t *testing.T, capture *capturedEmbeddingRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer k")
		}

		var raw map[string]json.RawMessage
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("fixture: read body: %v", err)
		}
		if err := json.Unmarshal(buf, &raw); err != nil {
			t.Fatalf("fixture: decode raw request: %v", err)
		}
		if capture != nil {
			if err := json.Unmarshal(buf, &capture.embeddingRequest); err != nil {
				t.Fatalf("fixture: decode typed request: %v", err)
			}
			capture.raw = raw
		}

		w.Header().Set("Content-Type", "application/json")
		resp := embeddingResponse{
			Data: []embeddingDataWire{
				{Embedding: []float64{0.1, 0.1}, Index: 0},
				{Embedding: []float64{0.2, 0.2}, Index: 1},
				{Embedding: []float64{0.3, 0.3}, Index: 2},
			},
			Usage: embeddingUsageWire{TotalTokens: 9},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEmbedRequestShape(t *testing.T) {
	var captured capturedEmbeddingRequest
	srv := newEmbeddingFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")

	_, err := model.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if captured.Model != "voyage-3" {
		t.Errorf("model = %q, want voyage-3", captured.Model)
	}
	if len(captured.Input) != 3 {
		t.Errorf("input = %v, want 3 entries", captured.Input)
	}
	if _, ok := captured.raw["input_type"]; ok {
		t.Errorf("input_type present in request body, want omitted when not set")
	}
	if _, ok := captured.raw["texts"]; ok {
		t.Errorf("texts present in request body, want voyage's field name is input, not texts")
	}
}

func TestEmbedCallProviderOptionsMerge(t *testing.T) {
	var captured capturedEmbeddingRequest
	srv := newEmbeddingFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")

	withOpts, ok := model.(provider.EmbeddingModelWithOptions)
	if !ok {
		t.Fatal("voyage embeddingModel does not implement provider.EmbeddingModelWithOptions")
	}

	_, err := withOpts.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values: []string{"a"},
		ProviderOptions: map[string]any{
			"voyage": map[string]any{
				"input_type":       "query",
				"output_dimension": 256,
			},
		},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}

	if captured.InputType != "query" {
		t.Errorf("input_type = %q, want query", captured.InputType)
	}
	raw, ok := captured.raw["output_dimension"]
	if !ok {
		t.Fatal("output_dimension missing from request body, want present via provider options passthrough")
	}
	if string(raw) != "256" {
		t.Errorf("output_dimension = %s, want 256", raw)
	}
}

func TestEmbedResultOrderFromIndexAndUsage(t *testing.T) {
	srv := newEmbeddingFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")

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
	if resp.Usage.TotalTokens != 9 {
		t.Errorf("Usage.TotalTokens = %d, want 9 (from usage.total_tokens)", resp.Usage.TotalTokens)
	}
}

func TestEmbedModelMetadata(t *testing.T) {
	srv := newEmbeddingFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")

	if got := model.ModelID(); got != "voyage-3" {
		t.Errorf("ModelID() = %q, want voyage-3", got)
	}
	if got := model.ProviderName(); got != "voyage" {
		t.Errorf("ProviderName() = %q, want voyage", got)
	}
	if got := model.MaxBatchSize(); got != 128 {
		t.Errorf("MaxBatchSize() = %d, want 128", got)
	}
}

func TestEmbedErrorPropagatesAPICallError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"detail":"bad request"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")
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

func TestEmbedContextCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/embeddings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(embeddingResponse{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("voyage-3")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Embed(ctx, []string{"a"})
	if err == nil {
		t.Fatal("Embed: want error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want wrapping context.Canceled", err)
	}
}
