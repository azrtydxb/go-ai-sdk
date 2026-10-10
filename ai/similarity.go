package ai

import "github.com/azrtydxb/go-ai-sdk/ai/embedding"

// CosineSimilarity returns the cosine similarity of two equal-length
// vectors: dot(a, b) / (||a|| * ||b||). It errors if a and b differ in
// length, or if either vector has zero magnitude (cosine similarity is
// undefined for a zero vector).
//
// Deprecated: use [embedding.CosineSimilarity] instead. This wrapper is
// kept so existing code keeps compiling; it will be removed in a future
// major version.
func CosineSimilarity(a, b []float64) (float64, error) {
	return embedding.CosineSimilarity(a, b)
}
