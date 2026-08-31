package provider

import "context"

// Capabilities reports what a model can do natively, so the SDK can choose
// between a provider-enforced path and a prompted fallback rather than
// guessing from the model ID.
type Capabilities struct {
	NativeJSON bool // supports schema-constrained JSON output natively
}

// LanguageModel is the interface every text-generating provider implements:
// one call, one stream, and enough identity to attribute and configure it.
// Everything in the ai package is built on top of this and nothing wider,
// which is what keeps a new provider a self-contained addition.
type LanguageModel interface {
	Generate(ctx context.Context, call Call) (*Response, error)
	Stream(ctx context.Context, call Call) (StreamResponse, error)
	ModelID() string
	ProviderName() string
	Capabilities() Capabilities
}

// EmbeddingResponse is one embedding request's vectors, in the same order
// as the input values, plus the token accounting for it.
type EmbeddingResponse struct {
	Embeddings [][]float64
	Usage      Usage
}

// EmbeddingModel is the interface embedding providers implement.
// MaxBatchSize is the provider's per-request ceiling; callers batching more
// values than that must split, since the model does not split for them.
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

	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
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
