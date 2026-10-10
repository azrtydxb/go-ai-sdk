package ai

import (
	"iter"

	"github.com/azrtydxb/go-ai-sdk/ai/embedding"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ChunkingWord and ChunkingLine are the recognized values for
// SmoothOpts.Chunking. Any other value (including the empty string) falls
// back to word chunking.
//
// Deprecated: use [embedding.ChunkingWord] and [embedding.ChunkingLine].
const (
	ChunkingWord = embedding.ChunkingWord
	ChunkingLine = embedding.ChunkingLine
)

// SmoothOpts configures SmoothStream.
//
// Deprecated: use [embedding.SmoothOpts] instead (a type alias, so values
// are interchangeable).
type SmoothOpts = embedding.SmoothOpts

// SmoothStream re-chunks text deltas from a StreamText/GenerateText parts
// sequence for smoother UI presentation, passing every non-text part
// through untouched.
//
// Deprecated: use [embedding.SmoothStream] instead. This wrapper is kept
// so existing code keeps compiling; it will be removed in a future major
// version.
func SmoothStream(parts iter.Seq[provider.StreamPart], opts SmoothOpts) iter.Seq[provider.StreamPart] {
	return embedding.SmoothStream(parts, opts)
}
