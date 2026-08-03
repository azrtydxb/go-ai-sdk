package cohere

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
// presence/absence of optional fields like top_n).
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
			Results: []rerankResultWire{
				{Index: 1, RelevanceScore: 0.95},
				{Index: 0, RelevanceScore: 0.2},
			},
			Meta: rerankMeta{BilledUnits: rerankBilledUnits{SearchUnits: 1}},
		}
		json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRerankRequestShapeTopNOmittedWhenZero(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "what is go",
		Documents: []string{"doc a", "doc b"},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	if captured.Model != "rerank-v3.5" {
		t.Errorf("model = %q, want rerank-v3.5", captured.Model)
	}
	if captured.Query != "what is go" {
		t.Errorf("query = %q, want %q", captured.Query, "what is go")
	}
	if len(captured.Documents) != 2 {
		t.Errorf("documents = %v, want 2 entries", captured.Documents)
	}
	if _, ok := captured.raw["top_n"]; ok {
		t.Errorf("top_n present in request body, want omitted when TopN == 0")
	}
}

func TestRerankRequestShapeTopNIncludedWhenNonZero(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "q",
		Documents: []string{"a", "b", "c"},
		TopN:      2,
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	raw, ok := captured.raw["top_n"]
	if !ok {
		t.Fatal("top_n missing from request body, want present when TopN != 0")
	}
	if string(raw) != "2" {
		t.Errorf("top_n = %s, want 2", raw)
	}
}

func TestRerankProviderOptionsMergeWins(t *testing.T) {
	var captured capturedRerankRequest
	srv := newRerankFixtureServer(t, &captured)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

	_, err := model.Rerank(context.Background(), provider.RerankCall{
		Query:     "q",
		Documents: []string{"a"},
		ProviderOptions: map[string]any{
			"cohere": map[string]any{
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

func TestRerankResultOrderPreserved(t *testing.T) {
	srv := newRerankFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

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
	if resp.Usage.TotalTokens != 0 {
		t.Errorf("Usage.TotalTokens = %d, want 0 (search units are not tokens)", resp.Usage.TotalTokens)
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw is empty, want raw response body")
	}
}

func TestRerankModelMetadata(t *testing.T) {
	srv := newRerankFixtureServer(t, nil)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

	if got := model.ModelID(); got != "rerank-v3.5" {
		t.Errorf("ModelID() = %q, want rerank-v3.5", got)
	}
	if got := model.ProviderName(); got != "cohere" {
		t.Errorf("ProviderName() = %q, want cohere", got)
	}
}

func TestRerankErrorPropagatesAPICallError401(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rerank", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"message":"unauthorized"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")
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
		w.Write([]byte(`{"message":"rate limited"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")
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

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).RerankingModel("rerank-v3.5")

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
