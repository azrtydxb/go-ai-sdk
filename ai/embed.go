package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// EmbedOpts options for the Embed function.
type EmbedOpts struct {
	Model      provider.EmbeddingModel
	Value      string
	MaxRetries *int
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
		return nil, fmt.Errorf("Model must not be nil")
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.EmbeddingResponse, error) {
		return opts.Model.Embed(ctx, []string{opts.Value})
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
		return nil, err
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
		return nil, fmt.Errorf("Model must not be nil")
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
	var allEmbeddings [][]float64
	var totalUsage provider.Usage

	// Split Values into batches and process each
	for i := 0; i < len(opts.Values); i += batchSize {
		end := i + batchSize
		if end > len(opts.Values) {
			end = len(opts.Values)
		}
		batch := opts.Values[i:end]

		resp, err := retry.Do(ctx, maxRetries, func() (*provider.EmbeddingResponse, error) {
			return opts.Model.Embed(ctx, batch)
		})
		if err != nil {
			var exhausted *retry.ExhaustedError
			if errors.As(err, &exhausted) {
				return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
			}
			return nil, err
		}

		allEmbeddings = append(allEmbeddings, resp.Embeddings...)
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
	}

	return &EmbedManyResult{
		Embeddings: allEmbeddings,
		Usage:      totalUsage,
	}, nil
}
