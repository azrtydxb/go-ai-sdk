package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// mockReranker is a provider.RerankingModel test double that returns
// canned responses in order, or fails with Err (for the first ErrCount
// calls) if set.
type mockReranker struct {
	Resp     *provider.RerankResponse
	Err      error
	ErrCount int // number of leading calls that fail with Err before succeeding
	Calls    []provider.RerankCall
}

func (m *mockReranker) Rerank(ctx context.Context, call provider.RerankCall) (*provider.RerankResponse, error) {
	m.Calls = append(m.Calls, call)
	if m.Err != nil && len(m.Calls) <= m.ErrCount {
		return nil, m.Err
	}
	return m.Resp, nil
}

func (m *mockReranker) ModelID() string      { return "mock-rerank" }
func (m *mockReranker) ProviderName() string { return "aitest" }

func TestRerankNilModel(t *testing.T) {
	_, err := Rerank(t.Context(), RerankOpts{Query: "q", Documents: []string{"a"}})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestRerankEmptyQuery(t *testing.T) {
	m := &mockReranker{}
	_, err := Rerank(t.Context(), RerankOpts{Model: m, Documents: []string{"a"}})
	if !errors.Is(err, ErrQueryRequired) {
		t.Fatalf("err = %v, want ErrQueryRequired", err)
	}
}

func TestRerankEmptyDocuments(t *testing.T) {
	m := &mockReranker{}
	_, err := Rerank(t.Context(), RerankOpts{Model: m, Query: "q"})
	if !errors.Is(err, ErrDocumentsRequired) {
		t.Fatalf("err = %v, want ErrDocumentsRequired", err)
	}
}

func TestRerankHappyPath(t *testing.T) {
	m := &mockReranker{
		Resp: &provider.RerankResponse{
			Results: []provider.RankedDocument{
				{Index: 2, Score: 0.9},
				{Index: 0, Score: 0.5},
			},
			Usage: provider.Usage{TotalTokens: 0},
		},
	}
	docs := []string{"doc-a", "doc-b", "doc-c"}
	res, err := Rerank(t.Context(), RerankOpts{Model: m, Query: "q", Documents: docs})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("Results = %d, want 2", len(res.Results))
	}
	if res.Results[0].Index != 2 || res.Results[0].Document != "doc-c" || res.Results[0].Score != 0.9 {
		t.Errorf("Results[0] = %+v, want {Index:2 Document:doc-c Score:0.9}", res.Results[0])
	}
	if res.Results[1].Index != 0 || res.Results[1].Document != "doc-a" || res.Results[1].Score != 0.5 {
		t.Errorf("Results[1] = %+v, want {Index:0 Document:doc-a Score:0.5}", res.Results[1])
	}
}

func TestRerankSkipsOutOfRangeIndex(t *testing.T) {
	m := &mockReranker{
		Resp: &provider.RerankResponse{
			Results: []provider.RankedDocument{
				{Index: 0, Score: 0.9},
				{Index: 5, Score: 0.8}, // out of range: only 2 docs
				{Index: -1, Score: 0.1},
			},
		},
	}
	docs := []string{"a", "b"}
	res, err := Rerank(t.Context(), RerankOpts{Model: m, Query: "q", Documents: docs})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("Results = %d, want 1 (out-of-range indexes skipped)", len(res.Results))
	}
	if res.Results[0].Index != 0 || res.Results[0].Document != "a" {
		t.Errorf("Results[0] = %+v, want {Index:0 Document:a}", res.Results[0])
	}
}

func TestRerankRetriesThenSucceeds(t *testing.T) {
	m := &mockReranker{
		Err:      NewAPICallError(429, "https://x", "", "rate limited"),
		ErrCount: 1,
		Resp: &provider.RerankResponse{
			Results: []provider.RankedDocument{{Index: 0, Score: 1}},
		},
	}
	res, err := Rerank(t.Context(), RerankOpts{Model: m, Query: "q", Documents: []string{"a"}})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(m.Calls) != 2 {
		t.Fatalf("Calls = %d, want 2 (1 failure + 1 retry)", len(m.Calls))
	}
	if len(res.Results) != 1 {
		t.Fatalf("Results = %d, want 1", len(res.Results))
	}
}

func TestRerankRetriesExhausted(t *testing.T) {
	m := &mockReranker{
		Err:      NewAPICallError(500, "https://x", "", "boom"),
		ErrCount: 100,
	}
	_, err := Rerank(t.Context(), RerankOpts{Model: m, Query: "q", Documents: []string{"a"}})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v, want RetryError{Attempts:3}", err)
	}
}

func TestRerankLifecycleCallbacksFireOnSuccess(t *testing.T) {
	m := &mockReranker{
		Resp: &provider.RerankResponse{
			Results: []provider.RankedDocument{{Index: 0, Score: 1}},
		},
	}
	var startCalls, endCalls int
	var startQuery string
	var startDocs []string
	var endResp *provider.RerankResponse
	var endErr error

	_, err := Rerank(t.Context(), RerankOpts{
		Model:     m,
		Query:     "q",
		Documents: []string{"a", "b"},
		OnRerankStart: func(query string, documents []string) {
			startCalls++
			startQuery = query
			startDocs = documents
		},
		OnRerankEnd: func(resp *provider.RerankResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if startCalls != 1 || endCalls != 1 {
		t.Fatalf("startCalls=%d endCalls=%d, want 1 each", startCalls, endCalls)
	}
	if startQuery != "q" || len(startDocs) != 2 {
		t.Errorf("OnRerankStart args = (%q, %v)", startQuery, startDocs)
	}
	if endResp == nil || endErr != nil {
		t.Errorf("OnRerankEnd args = (%v, %v), want (non-nil, nil)", endResp, endErr)
	}
}

func TestRerankLifecycleCallbacksFireOnFinalError(t *testing.T) {
	m := &mockReranker{
		Err:      NewAPICallError(500, "https://x", "", "boom"),
		ErrCount: 100,
	}
	var startCalls, endCalls int
	var endErr error

	_, err := Rerank(t.Context(), RerankOpts{
		Model:         m,
		Query:         "q",
		Documents:     []string{"a"},
		MaxRetries:    intPtr(0),
		OnRerankStart: func(query string, documents []string) { startCalls++ },
		OnRerankEnd: func(resp *provider.RerankResponse, err error) {
			endCalls++
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 || endCalls != 1 {
		t.Fatalf("startCalls=%d endCalls=%d, want 1 each", startCalls, endCalls)
	}
	if endErr == nil {
		t.Error("OnRerankEnd err = nil, want non-nil")
	}
}

func intPtr(i int) *int { return &i }
