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
