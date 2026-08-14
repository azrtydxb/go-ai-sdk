package ai

import (
	"context"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrQueryRequired is returned when Query is empty in RerankOpts.
var ErrQueryRequired = errors.New("ai: query is required")

// ErrDocumentsRequired is returned when Documents is empty in RerankOpts.
var ErrDocumentsRequired = errors.New("ai: documents is required")

// RerankOpts options for the Rerank function.
type RerankOpts struct {
	Model           provider.RerankingModel // required
	Query           string                  // required
	Documents       []string                // required, non-empty
	TopN            int
	MaxRetries      *int
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.RerankCall.Headers unchanged — see that field's
	// doc for precedence (it never overrides the provider's auth header)
	// and which request paths implement it.
	Headers map[string]string

	// OnRerankStart, when non-nil, fires once before the first attempt.
	OnRerankStart func(query string, documents []string)
	// OnRerankEnd, when non-nil, fires once after the final attempt
	// (success or exhausted error). err, when non-nil, is the SAME error
	// Rerank itself returns for that failure (retry exhaustion translated
	// to *RetryError, never the raw retry-internal error). resp is nil on
	// error.
	OnRerankEnd func(resp *provider.RerankResponse, err error)
}

// RankedDocument mirrors provider.RankedDocument plus the resolved document text.
type RankedDocument struct {
	Index    int
	Score    float64
	Document string
}

// RerankResult is the outcome of a Rerank call.
type RerankResult struct {
	Results []RankedDocument
	Usage   provider.Usage
}

// Rerank ranks opts.Documents by relevance to opts.Query using the
// provided model. It wraps the call in retry logic (default maxRetries = 2).
func Rerank(ctx context.Context, opts RerankOpts) (*RerankResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if opts.Query == "" {
		return nil, ErrQueryRequired
	}
	if len(opts.Documents) == 0 {
		return nil, ErrDocumentsRequired
	}

	call := provider.RerankCall{
		Query:           opts.Query,
		Documents:       opts.Documents,
		TopN:            opts.TopN,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	// OnRerankStart takes (query, documents) rather than the call struct, so
	// adapt it to mediaCall's onStart shape. OnRerankEnd sees the SAME error
	// the caller gets (the translated *RetryError on retry exhaustion) — not
	// the raw *retry.ExhaustedError from retry.Do.
	var onStart func(provider.RerankCall)
	if opts.OnRerankStart != nil {
		onStart = func(c provider.RerankCall) { opts.OnRerankStart(c.Query, c.Documents) }
	}

	resp, err := mediaCall(ctx, opts.MaxRetries, call, onStart, opts.Model.Rerank, opts.OnRerankEnd)
	if err != nil {
		return nil, err
	}

	results := make([]RankedDocument, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Index < 0 || r.Index >= len(opts.Documents) {
			// Defensive: skip out-of-range indices from the provider
			// rather than panic on opts.Documents[r.Index].
			continue
		}
		results = append(results, RankedDocument{
			Index:    r.Index,
			Score:    r.Score,
			Document: opts.Documents[r.Index],
		})
	}

	return &RerankResult{
		Results: results,
		Usage:   resp.Usage,
	}, nil
}
