package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func init() {
	retry.BaseDelay = time.Millisecond
}

// TestEmbedManyBatchesAndReassembles from the brief
func TestEmbedManyBatchesAndReassembles(t *testing.T) {
	m := &aitest.MockEmbedder{BatchSize: 2}
	res, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a", "bb", "ccc", "dddd", "e"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Batches) != 3 {
		t.Fatalf("batches = %d, want 3 (2+2+1)", len(m.Batches))
	}
	if len(res.Embeddings) != 5 {
		t.Fatalf("embeddings = %d", len(res.Embeddings))
	}
	if res.Embeddings[2][0] != 3 {
		t.Fatalf("order broken: %v", res.Embeddings[2])
	}
	if res.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

// TestEmbedSingle from the brief
func TestEmbedSingle(t *testing.T) {
	m := &aitest.MockEmbedder{}
	res, err := Embed(t.Context(), EmbedOpts{Model: m, Value: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedding[0] != 3 {
		t.Fatalf("embedding = %v", res.Embedding)
	}
}

// TestEmbedManyEmptyValues: empty Values slice returns empty result with no model call
func TestEmbedManyEmptyValues(t *testing.T) {
	m := &aitest.MockEmbedder{}
	res, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Embeddings) != 0 {
		t.Fatalf("embeddings = %d, want 0", len(res.Embeddings))
	}
	if len(m.Batches) != 0 {
		t.Fatalf("batches = %d, want 0 (no model call)", len(m.Batches))
	}
	if res.Usage.TotalTokens != 0 {
		t.Fatalf("usage.TotalTokens = %d, want 0", res.Usage.TotalTokens)
	}
}

// TestEmbedManyNilModel: Model must be non-nil
func TestEmbedManyNilModel(t *testing.T) {
	_, err := EmbedMany(t.Context(), EmbedManyOpts{Values: []string{"a"}})
	if err == nil {
		t.Fatal("want error when Model is nil")
	}
}

// TestEmbedNilModel: Model must be non-nil
func TestEmbedNilModel(t *testing.T) {
	_, err := Embed(t.Context(), EmbedOpts{Value: "a"})
	if err == nil {
		t.Fatal("want error when Model is nil")
	}
}

// TestEmbedManyRetriesOnRetryableError: retries on 500, then wraps in RetryError after exhaustion
func TestEmbedManyRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockEmbedder{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a"}})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Batches) != 3 {
		t.Fatalf("batches = %d, want 3 (1 + 2 retries)", len(m.Batches))
	}
}

// TestEmbedRetriesOnRetryableError: retries on 500, then wraps in RetryError after exhaustion
func TestEmbedRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockEmbedder{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := Embed(t.Context(), EmbedOpts{Model: m, Value: "a"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Batches) != 3 {
		t.Fatalf("batches = %d, want 3 (1 + 2 retries)", len(m.Batches))
	}
}

// TestEmbedManyGuardsBatchSizeZero: MaxBatchSize() returning 0 doesn't hang
func TestEmbedManyGuardsBatchSizeZero(t *testing.T) {
	m := &testBatchSizeZeroEmbedder{}
	res, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Embeddings) != 3 {
		t.Fatalf("embeddings = %d, want 3", len(res.Embeddings))
	}
	// Guard should make batch size 1, so 3 calls for 3 values
	if len(m.batches) != 3 {
		t.Fatalf("batches = %d, want 3 (guard makes batch size 1)", len(m.batches))
	}
}

// TestEmbedManyGuardsBatchSizeNegative: MaxBatchSize() returning negative doesn't panic
func TestEmbedManyGuardsBatchSizeNegative(t *testing.T) {
	m := &testBatchSizeNegativeEmbedder{}
	res, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Embeddings) != 2 {
		t.Fatalf("embeddings = %d, want 2", len(res.Embeddings))
	}
	// Guard should make batch size 1, so 2 calls for 2 values
	if len(m.batches) != 2 {
		t.Fatalf("batches = %d, want 2 (guard makes batch size 1)", len(m.batches))
	}
}

// TestEmbedManyShortResponse: fewer embeddings than requested returns error
func TestEmbedManyShortResponse(t *testing.T) {
	m := &testShortResponseEmbedder{}
	_, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a", "b", "c"}})
	if err == nil {
		t.Fatal("want error when model returns fewer embeddings than requested")
	}
	if !strings.Contains(err.Error(), "returned 1 embeddings for 2 values") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestEmbedShortResponse: fewer embeddings than requested returns error
func TestEmbedShortResponse(t *testing.T) {
	m := &testEmptyResponseEmbedder{}
	_, err := Embed(t.Context(), EmbedOpts{Model: m, Value: "a"})
	if err == nil {
		t.Fatal("want error when model returns zero embeddings")
	}
	if !strings.Contains(err.Error(), "returned 0 embeddings") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// testBatchSizeZeroEmbedder returns 0 from MaxBatchSize()
type testBatchSizeZeroEmbedder struct {
	batches [][]string
}

func (m *testBatchSizeZeroEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	m.batches = append(m.batches, values)
	embeddings := make([][]float64, len(values))
	for i := range embeddings {
		embeddings[i] = []float64{1, 2, 3}
	}
	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage:      provider.Usage{TotalTokens: len(values)},
	}, nil
}

func (m *testBatchSizeZeroEmbedder) MaxBatchSize() int { return 0 }
func (m *testBatchSizeZeroEmbedder) ModelID() string   { return "test-zero" }
func (m *testBatchSizeZeroEmbedder) ProviderName() string {
	return "test"
}

// testBatchSizeNegativeEmbedder returns negative from MaxBatchSize()
type testBatchSizeNegativeEmbedder struct {
	batches [][]string
}

func (m *testBatchSizeNegativeEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	m.batches = append(m.batches, values)
	embeddings := make([][]float64, len(values))
	for i := range embeddings {
		embeddings[i] = []float64{1, 2, 3}
	}
	return &provider.EmbeddingResponse{
		Embeddings: embeddings,
		Usage:      provider.Usage{TotalTokens: len(values)},
	}, nil
}

func (m *testBatchSizeNegativeEmbedder) MaxBatchSize() int { return -5 }
func (m *testBatchSizeNegativeEmbedder) ModelID() string   { return "test-neg" }
func (m *testBatchSizeNegativeEmbedder) ProviderName() string {
	return "test"
}

// testShortResponseEmbedder returns fewer embeddings than requested (1 for batch size 2)
type testShortResponseEmbedder struct {
}

func (m *testShortResponseEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	// Always return 1 embedding regardless of batch size
	return &provider.EmbeddingResponse{
		Embeddings: [][]float64{{1, 2, 3}},
		Usage:      provider.Usage{TotalTokens: 1},
	}, nil
}

func (m *testShortResponseEmbedder) MaxBatchSize() int { return 2 }
func (m *testShortResponseEmbedder) ModelID() string   { return "test-short" }
func (m *testShortResponseEmbedder) ProviderName() string {
	return "test"
}

// testEmptyResponseEmbedder returns no embeddings
type testEmptyResponseEmbedder struct {
}

func (m *testEmptyResponseEmbedder) Embed(ctx context.Context, values []string) (*provider.EmbeddingResponse, error) {
	// Return no embeddings
	return &provider.EmbeddingResponse{
		Embeddings: [][]float64{},
		Usage:      provider.Usage{TotalTokens: 0},
	}, nil
}

func (m *testEmptyResponseEmbedder) MaxBatchSize() int { return 1 }
func (m *testEmptyResponseEmbedder) ModelID() string   { return "test-empty" }
func (m *testEmptyResponseEmbedder) ProviderName() string {
	return "test"
}
