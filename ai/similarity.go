package ai

import (
	"fmt"
	"math"
)

// CosineSimilarity returns the cosine similarity of two equal-length
// vectors: dot(a, b) / (||a|| * ||b||). It errors if a and b differ in
// length, or if either vector has zero magnitude (cosine similarity is
// undefined for a zero vector).
func CosineSimilarity(a, b []float64) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("ai: cosine similarity vectors have different lengths (%d != %d)", len(a), len(b))
	}

	var dot, magA, magB float64
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}
	magA = math.Sqrt(magA)
	magB = math.Sqrt(magB)

	if magA == 0 || magB == 0 {
		return 0, fmt.Errorf("ai: cosine similarity undefined for zero-magnitude vector")
	}

	return dot / (magA * magB), nil
}
