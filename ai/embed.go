package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.EmbeddingCall.Headers unchanged — see that
	// field's doc for precedence (it never overrides the provider's auth
	// header) and which request paths implement it. It only has an effect
	// when Model implements provider.EmbeddingModelWithOptions; it is
	// silently ignored otherwise.
	Headers map[string]string

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
		return embedCall(ctx, opts.Model, values, opts.ProviderOptions, opts.Headers)
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

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.EmbeddingCall.Headers unchanged — see that
	// field's doc for precedence (it never overrides the provider's auth
	// header) and which request paths implement it. It only has an effect
	// when Model implements provider.EmbeddingModelWithOptions; it is
	// silently ignored otherwise.
	Headers map[string]string

	// Concurrency bounds how many batches may be in flight at once. 0 or 1
	// (the default) processes batches strictly sequentially, identical to
	// pre-Concurrency behavior. A value greater than 1 fans batches out over
	// a worker pool of at most Concurrency goroutines: each batch is still
	// retried independently via the same retry.Do + translateRetryErr path
	// as the sequential mode, but batches run concurrently and results are
	// reassembled index-aligned (Embeddings stays aligned with Values;
	// Usage is summed across all batches regardless of completion order).
	//
	// On the first batch failure, the context used for all other batches is
	// cancelled via context.WithCancel: in-flight batches are allowed to
	// drain (EmbedMany waits for every dispatched batch to return before
	// returning itself) but no new batches are dispatched once cancellation
	// is observed. EmbedMany returns the first error encountered (in
	// completion order, not batch order), translated the same way the
	// sequential path translates it.
	//
	// Callback contract under concurrency: OnEmbedStart/OnEmbedEnd still
	// fire exactly once per batch, but from worker goroutines, in
	// completion order rather than batch order — callers that set these
	// callbacks with Concurrency > 1 MUST make them goroutine-safe (e.g.
	// guard shared state with a mutex or use atomics). In sequential mode
	// (0 or 1) the existing in-order, single-goroutine guarantee holds
	// verbatim.
	Concurrency int

	// OnEmbedStart, when non-nil, fires once per underlying provider call —
	// once per batch — before the first attempt of that batch. See
	// Concurrency's doc for the ordering/goroutine-safety contract this
	// callback must satisfy when Concurrency > 1.
	OnEmbedStart func(values []string)
	// OnEmbedEnd, when non-nil, fires once per batch after the final
	// attempt of that batch (success or retry exhaustion). err, when
	// non-nil, is the SAME error EmbedMany itself returns for that failure
	// (retry exhaustion translated to *RetryError). resp is nil on error.
	// See Concurrency's doc for the ordering/goroutine-safety contract this
	// callback must satisfy when Concurrency > 1.
	OnEmbedEnd func(resp *provider.EmbeddingResponse, err error)
}

// embedCall calls model.Embed, or model.EmbedCall (with providerOptions and
// headers) when model implements provider.EmbeddingModelWithOptions and
// either providerOptions or headers is non-empty. A model that does not
// implement provider.EmbeddingModelWithOptions silently ignores both.
func embedCall(ctx context.Context, model provider.EmbeddingModel, values []string, providerOptions map[string]any, headers map[string]string) (*provider.EmbeddingResponse, error) {
	if len(providerOptions) > 0 || len(headers) > 0 {
		if optioned, ok := model.(provider.EmbeddingModelWithOptions); ok {
			return optioned.EmbedCall(ctx, provider.EmbeddingCall{Values: values, ProviderOptions: providerOptions, Headers: headers})
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

	if opts.Concurrency > 1 {
		return embedManyConcurrent(ctx, opts, batchSize, maxRetries)
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
			return embedCall(ctx, opts.Model, batch, opts.ProviderOptions, opts.Headers)
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

// embedManyConcurrent implements EmbedMany's Concurrency > 1 path. Batches
// are split the same way the sequential path splits them, then fanned out
// over a worker pool bounded by opts.Concurrency using a buffered-channel
// semaphore (stdlib only, no external worker-pool package). Each batch is
// retried independently via the same retry.Do + translateRetryErr sequence
// the sequential path uses, and OnEmbedStart/OnEmbedEnd fire once per batch
// from whichever goroutine handles it — see EmbedManyOpts.Concurrency's doc
// for the resulting ordering and goroutine-safety contract.
//
// Results are written into an index-aligned slice so reassembly does not
// depend on completion order. On the first batch error, a shared
// context.WithCancel is cancelled so in-flight retries/calls observe
// cancellation and return promptly, and the dispatch loop stops launching
// new batches; a sync.WaitGroup ensures every already-dispatched batch
// drains before EmbedMany returns the first error encountered.
func embedManyConcurrent(ctx context.Context, opts EmbedManyOpts, batchSize, maxRetries int) (*EmbedManyResult, error) {
	var batches [][]string
	for i := 0; i < len(opts.Values); i += batchSize {
		end := i + batchSize
		if end > len(opts.Values) {
			end = len(opts.Values)
		}
		batches = append(batches, opts.Values[i:end])
	}

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	embeddings := make([][][]float64, len(batches))
	usages := make([]provider.Usage, len(batches))

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	var errOnce sync.Once
	var firstErr error

	for i, batch := range batches {
		// Block for a free worker slot, then re-check cancellation before
		// dispatching: once a prior batch has failed, don't start new work.
		sem <- struct{}{}
		if cctx.Err() != nil {
			<-sem
			break
		}

		wg.Add(1)
		go func(i int, batch []string) {
			defer wg.Done()
			defer func() { <-sem }()

			if opts.OnEmbedStart != nil {
				opts.OnEmbedStart(batch)
			}

			resp, err := retry.Do(cctx, maxRetries, func() (*provider.EmbeddingResponse, error) {
				return embedCall(cctx, opts.Model, batch, opts.ProviderOptions, opts.Headers)
			})
			callErr := translateRetryErr(err)

			// Mirror the sequential path exactly: OnEmbedEnd sees callErr
			// as translated from retry.Do BEFORE the embeddings-count
			// mismatch check below, so (like sequential mode) a mismatch
			// is reported to the caller via EmbedMany's return value but
			// NOT re-surfaced to OnEmbedEnd.
			if opts.OnEmbedEnd != nil {
				opts.OnEmbedEnd(resp, callErr)
			}

			if callErr != nil {
				errOnce.Do(func() {
					firstErr = callErr
					cancel()
				})
				return
			}

			if len(resp.Embeddings) != len(batch) {
				errOnce.Do(func() {
					firstErr = fmt.Errorf("ai: embedding model returned %d embeddings for %d values", len(resp.Embeddings), len(batch))
					cancel()
				})
				return
			}

			embeddings[i] = resp.Embeddings
			usages[i] = resp.Usage
		}(i, batch)
	}

	wg.Wait()

	// If the parent context was cancelled (or deadline-exceeded) after every
	// dispatched batch had already succeeded, firstErr is still nil here even
	// though not all Values were embedded — the dispatch loop above breaks
	// out of the for-range without processing the remaining batches. Without
	// this check, EmbedMany would silently return a truncated Embeddings
	// slice with a nil error. Surface the parent ctx's error the same way
	// the sequential path does (retry.Do returns ctx.Err() unwrapped, and
	// translateRetryErr passes it through unchanged).
	if firstErr == nil {
		if cerr := ctx.Err(); cerr != nil {
			firstErr = translateRetryErr(cerr)
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	var allEmbeddings [][]float64
	var totalUsage provider.Usage
	for i := range embeddings {
		allEmbeddings = append(allEmbeddings, embeddings[i]...)
		totalUsage.InputTokens += usages[i].InputTokens
		totalUsage.OutputTokens += usages[i].OutputTokens
		totalUsage.TotalTokens += usages[i].TotalTokens
		totalUsage.CachedInputTokens += usages[i].CachedInputTokens
		totalUsage.ReasoningTokens += usages[i].ReasoningTokens
	}

	return &EmbedManyResult{
		Embeddings: allEmbeddings,
		Usage:      totalUsage,
	}, nil
}
