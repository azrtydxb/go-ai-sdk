package ai

import (
	"errors"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/internal/retry"
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
