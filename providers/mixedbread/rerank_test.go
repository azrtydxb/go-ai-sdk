package mixedbread

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

// capturedRerankRequest holds both the typed decode of a captured request
// body (for asserting field values) and the raw JSON object (for asserting
// presence/absence of optional fields like top_k).
type capturedRerankRequest struct {
	rerankRequest
	raw map[string]json.RawMessage
}

func newRerankFixtureServer(t *testing.T, capture *capturedRerankRequest) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer k")
		}

		var raw map[string]json.RawMessage
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("fixture: read body: %v", err)
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("fixture: decode raw request: %v", err)
		}
		if capture != nil {
			if err := json.Unmarshal(body, &capture.rerankRequest); err != nil {
				t.Fatalf("fixture: decode typed request: %v", err)
			}
			capture.raw = raw
		}

		w.Header().Set("Content-Type", "application/json")
		resp := rerankResponse{
			Data: []rerankResultWire{
				{Index: 1, Score: 0.95},
				{Index: 0, Score: 0.2},
			},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRerankRequestShape(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "what is go",
		Documents: []string{"doc a", "doc b"},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	if captured.Model != "mixedbread-ai/mxbai-rerank-large-v1" {
		t.Errorf("model = %q, want mixedbread-ai/mxbai-rerank-large-v1", captured.Model)
	}
	if captured.Query != "what is go" {
		t.Errorf("query = %q, want %q", captured.Query, "what is go")
	}
	if len(captured.Input) != 2 {
		t.Errorf("input = %v, want 2 entries", captured.Input)
	}

	// The field-name differences the task brief calls out: Mixedbread uses
	// "input", not "documents"; and always sends "return_input".
	if _, ok := captured.raw["input"]; !ok {
		t.Error(`input field missing from request body, want present (mixedbread uses "input", not "documents")`)
	}
	if _, ok := captured.raw["documents"]; ok {
		t.Error(`documents field present in request body, want absent (mixedbread rerank uses "input", not "documents" — that's Voyage's/Cohere's field name)`)
	}
	riRaw, ok := captured.raw["return_input"]
	if !ok {
		t.Fatal("return_input missing from request body, want present (mixedbread-specific field)")
	}
	if string(riRaw) != "false" {
		t.Errorf("return_input = %s, want false", riRaw)
	}
	if _, ok := captured.raw["top_k"]; ok {
		t.Errorf("top_k present in request body, want omitted when TopN == 0")
	}
}

func TestRerankRequestTopKIncludedWhenNonZero(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "q",
		Documents: []string{"a", "b", "c"},
		TopN:      2,
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	raw, ok := captured.raw["top_k"]
	if !ok {
		t.Fatal("top_k missing from request body, want present when TopN != 0")
	}
	if string(raw) != "2" {
		t.Errorf("top_k = %s, want 2", raw)
	}
}

func TestRerankProviderOptionsMergeWins(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "q",
		Documents: []string{"a"},
		ProviderOptions: map[string]any{
			"mixedbread": map[string]any{
				"model": "rerank-override",
			},
		},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if captured.Model != "rerank-override" {
		t.Errorf("model = %q, want rerank-override (provider options should win)", captured.Model)
	}
}

// TestRerankResponseShapeUsesScoreNotRelevanceScore proves the response
// field-name difference the task brief calls out: Mixedbread's response
// uses "score", not "relevance_score" (Cohere's/Voyage's field name). A
// decoder built for the relevance_score shape would silently read a zero
// value from this fixture's payload; this test asserts the actual mapped
// scores instead, so it would fail if rerankResultWire used the wrong
// field name.
func TestRerankResponseShapeUsesScoreNotRelevanceScore(t *testing.T) {
	srv := newRerankFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	resp, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "q",
		Documents: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("Results = %d, want 2", len(resp.Results))
	}
	if resp.Results[0].Index != 1 || resp.Results[0].Score != 0.95 {
		t.Errorf("Results[0] = %+v, want {Index:1 Score:0.95}", resp.Results[0])
	}
	if resp.Results[1].Index != 0 || resp.Results[1].Score != 0.2 {
		t.Errorf("Results[1] = %+v, want {Index:0 Score:0.2}", resp.Results[1])
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw is empty, want raw response body")
	}
}

func TestRerankModelMetadata(t *testing.T) {
	srv := newRerankFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	if got := model.ModelID(); got != "mixedbread-ai/mxbai-rerank-large-v1" {
		t.Errorf("ModelID() = %q, want mixedbread-ai/mxbai-rerank-large-v1", got)
	}
	if got := model.ProviderName(); got != "mixedbread" {
		t.Errorf("ProviderName() = %q, want mixedbread", got)
	}
}

func TestRerankErrorPropagatesAPICallError401(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"detail":"unauthorized"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")
	_, err := model.Rerank(context.Background(), provider.RerankCall{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("Rerank: want error, got nil")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *ai.APICallError", err, err)
	}
	if apiErr.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if apiErr.Retryable {
		t.Error("Retryable = true, want false for 401")
	}
}

func TestRerankErrorPropagatesAPICallError429Retryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"detail":"rate limited"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")
	_, err := model.Rerank(context.Background(), provider.RerankCall{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("Rerank: want error, got nil")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *ai.APICallError", err, err)
	}
	if apiErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", apiErr.StatusCode)
	}
	if !apiErr.Retryable {
		t.Error("Retryable = false, want true for 429")
	}
}

func TestRerankContextCancel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rerankResponse{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := model.Rerank(ctx, provider.RerankCall{Query: "q", Documents: []string{"a"}})
	if err == nil {
		t.Fatal("Rerank: want error for cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want wrapping context.Canceled", err)
	}
}
