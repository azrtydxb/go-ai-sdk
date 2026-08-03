package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrModelRequired is returned when Model is nil in Embed or EmbedMany options.
var ErrModelRequired = errors.New("ai: model is required")

// EmbedOpts options for the Embed function.
type EmbedOpts struct {
	Model      provider.EmbeddingModel
	Value      string
	MaxRetries *int

	// ProviderOptions follows provider.Call.ProviderOptions' merge
	// semantics. It only has an effect when Model implements
	// provider.EmbeddingModelWithOptions; it is silently ignored otherwise.
	ProviderOptions map[string]any

	// OnEmbedStart, when non-nil, fires once before the first attempt of
	// the underlying provider call.
	OnEmbedStart func(values []string)
	// OnEmbedEnd, when non-nil, fires once after the final attempt (success
	// or retry exhaustion). err, when non-nil, is the SAME error Embed
	// itself returns (retry exhaustion translated to *RetryError). resp is
	// nil on error.
	OnEmbedEnd func(resp *provider.EmbeddingResponse, err error)
}

// EmbedResult is the outcome of an Embed call.
type EmbedResult struct {
	Embedding []float64
	Usage     provider.Usage
}

// Embed embeds a single string value using the provided model.
// It wraps the call in retry logic (default maxRetries = 2).
func Embed(ctx context.Context, opts EmbedOpts) (*EmbedResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	values := []string{opts.Value}
	if opts.OnEmbedStart != nil {
		opts.OnEmbedStart(values)
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.EmbeddingResponse, error) {
		return embedCall(ctx, opts.Model, values, opts.ProviderOptions)
	})
	callErr := translateRetryErr(err)

	if opts.OnEmbedEnd != nil {
		opts.OnEmbedEnd(resp, callErr)
	}
	if callErr != nil {
		return nil, callErr
	}

	if len(resp.Embeddings) < 1 {
		return nil, fmt.Errorf("ai: embedding model returned %d embeddings for 1 value", len(resp.Embeddings))
	}

	return &EmbedResult{
		Embedding: resp.Embeddings[0],
		Usage:     resp.Usage,
	}, nil
}

// EmbedManyOpts options for the EmbedMany function.
type EmbedManyOpts struct {
	Model      provider.EmbeddingModel
	Values     []string
	MaxRetries *int

	// ProviderOptions follows provider.Call.ProviderOptions' merge
	// semantics. It only has an effect when Model implements
	// provider.EmbeddingModelWithOptions; it is silently ignored otherwise.
	ProviderOptions map[string]any

	// OnEmbedStart, when non-nil, fires once per underlying provider call —
	// once per batch, in batch order — before the first attempt of that
	// batch.
	OnEmbedStart func(values []string)
	// OnEmbedEnd, when non-nil, fires once per batch (in batch order) after
	// the final attempt of that batch (success or retry exhaustion). err,
	// when non-nil, is the SAME error EmbedMany itself returns for that
	// failure (retry exhaustion translated to *RetryError). resp is nil on
	// error.
	OnEmbedEnd func(resp *provider.EmbeddingResponse, err error)
}

// embedCall calls model.Embed, or model.EmbedCall (with providerOptions)
// when model implements provider.EmbeddingModelWithOptions and providerOptions is
// non-empty.
func embedCall(ctx context.Context, model provider.EmbeddingModel, values []string, providerOptions map[string]any) (*provider.EmbeddingResponse, error) {
	if len(providerOptions) > 0 {
		if optioned, ok := model.(provider.EmbeddingModelWithOptions); ok {
			return optioned.EmbedCall(ctx, provider.EmbeddingCall{Values: values, ProviderOptions: providerOptions})
		}
	}
	return model.Embed(ctx, values)
}

// EmbedManyResult is the outcome of an EmbedMany call.
type EmbedManyResult struct {
	Embeddings [][]float64 // index-aligned with Values
	Usage      provider.Usage
}

// EmbedMany embeds multiple string values using the provided model.
// It splits Values into chunks of model.MaxBatchSize(), calls sequentially
// (each retried), reassembles in order, and sums usage.
// If Values is empty, returns empty result without calling the model.
func EmbedMany(ctx context.Context, opts EmbedManyOpts) (*EmbedManyResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}

	if len(opts.Values) == 0 {
		return &EmbedManyResult{
			Embeddings: [][]float64{},
			Usage:      provider.Usage{},
		}, nil
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	batchSize := opts.Model.MaxBatchSize()
	// Guard against zero or negative batch sizes to prevent infinite loops or panics
	if batchSize <= 0 {
		batchSize = 1
	}

	var allEmbeddings [][]float64
	var totalUsage provider.Usage

	// Split Values into batches and process each
	for i := 0; i < len(opts.Values); i += batchSize {
		end := i + batchSize
		if end > len(opts.Values) {
			end = len(opts.Values)
		}
		batch := opts.Values[i:end]

		if opts.OnEmbedStart != nil {
			opts.OnEmbedStart(batch)
		}

		resp, err := retry.Do(ctx, maxRetries, func() (*provider.EmbeddingResponse, error) {
			return embedCall(ctx, opts.Model, batch, opts.ProviderOptions)
		})
		callErr := translateRetryErr(err)

		if opts.OnEmbedEnd != nil {
			opts.OnEmbedEnd(resp, callErr)
		}
		if callErr != nil {
			return nil, callErr
		}

		// Validate that the model returned the expected number of embeddings
		if len(resp.Embeddings) != len(batch) {
			return nil, fmt.Errorf("ai: embedding model returned %d embeddings for %d values", len(resp.Embeddings), len(batch))
		}

		allEmbeddings = append(allEmbeddings, resp.Embeddings...)
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		totalUsage.CachedInputTokens += resp.Usage.CachedInputTokens
		totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens
	}

	return &EmbedManyResult{
		Embeddings: allEmbeddings,
		Usage:      totalUsage,
	}, nil
}
