package ai_test

import (
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// The ai-level CosineSimilarity/SmoothStream wrappers are deprecated but
// still part of the compiling API until the next major version; these
// checks fail if the delegation to ai/embedding breaks.
func TestDeprecatedCosineSimilarityDelegates(t *testing.T) {
	same, err := ai.CosineSimilarity([]float64{1, 0, 0}, []float64{1, 0, 0})
	if err != nil || same != 1 {
		t.Fatalf("identical vectors: got (%v, %v), want (1, nil)", same, err)
	}
	if _, err := ai.CosineSimilarity([]float64{1}, []float64{1, 2}); err == nil {
		t.Fatal("mismatched lengths: want error")
	}
}

func TestDeprecatedSmoothStreamDelegates(t *testing.T) {
	parts := []provider.StreamPart{
		provider.TextDelta{Text: "hello "},
		provider.TextDelta{Text: "world"},
	}
	seq := func(yield func(provider.StreamPart) bool) {
		for _, p := range parts {
			if !yield(p) {
				return
			}
		}
	}
	var got []string
	for p := range ai.SmoothStream(seq, ai.SmoothOpts{Chunking: ai.ChunkingWord}) {
		if td, ok := p.(provider.TextDelta); ok {
			got = append(got, td.Text)
		}
	}
	if len(got) == 0 {
		t.Fatal("SmoothStream emitted no text deltas")
	}
}
