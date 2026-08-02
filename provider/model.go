package provider

import "context"

type Capabilities struct {
	NativeJSON bool // supports schema-constrained JSON output natively
}

type LanguageModel interface {
	Generate(ctx context.Context, call Call) (*Response, error)
	Stream(ctx context.Context, call Call) (StreamResponse, error)
	ModelID() string
	ProviderName() string
	Capabilities() Capabilities
}

type EmbeddingResponse struct {
	Embeddings [][]float64
	Usage      Usage
}

type EmbeddingModel interface {
	Embed(ctx context.Context, values []string) (*EmbeddingResponse, error)
	MaxBatchSize() int
	ModelID() string
	ProviderName() string
}

// EmbeddingCall is the input to EmbeddingModelWithOptions.EmbedCall.
type EmbeddingCall struct {
	Values []string

	// ProviderOptions follows the same merge semantics as Call.ProviderOptions:
	// keyed by provider name, shallow-merged into the request body built for
	// the matching key, option entries winning.
	ProviderOptions map[string]any
}

// EmbeddingModelWithOptions is an optional extension of EmbeddingModel implemented by
// embedding models that support per-call ProviderOptions. It exists as a
// separate interface (rather than changing EmbeddingModel.Embed's signature)
// to keep the change additive; callers that want ProviderOptions support
// should type-assert to this interface, falling back to plain Embed when a
// model doesn't implement it.
type EmbeddingModelWithOptions interface {
	EmbeddingModel
	EmbedCall(ctx context.Context, call EmbeddingCall) (*EmbeddingResponse, error)
}
