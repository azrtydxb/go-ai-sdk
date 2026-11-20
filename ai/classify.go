package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrInputsRequired is returned when Inputs is empty in ClassifyOpts.
var ErrInputsRequired = errors.New("ai: inputs is required")

// ClassifyOpts options for the Classify function.
type ClassifyOpts struct {
	Model           provider.ClassificationModel // required
	Inputs          []string                     // required, non-empty
	MaxRetries      *int
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.ClassifyCall.Headers unchanged — see that field's
	// doc for precedence (it never overrides the provider's auth header).
	Headers map[string]string

	// OnClassifyStart, when non-nil, fires once before the first attempt.
	OnClassifyStart func(inputs []string)
	// OnClassifyEnd, when non-nil, fires once after the final attempt
	// (success or exhausted error). err, when non-nil, is the SAME error
	// Classify itself returns for that failure (retry exhaustion translated
	// to *RetryError). resp is nil on error.
	OnClassifyEnd func(resp *provider.ClassifyResponse, err error)
}

// ClassifyResult is the outcome of a Classify call.
type ClassifyResult struct {
	// Results holds one Classification per input, in input order.
	Results []provider.Classification
	Usage   provider.Usage
}

// Classify scores each of opts.Inputs with a sequence-classification model
// (for example a prompt-injection classifier), returning per-label scores
// for every input. It wraps the call in retry logic (default maxRetries =
// 2), like Rerank. A provider response that does not carry exactly one
// result per input is an error rather than a silently short result.
func Classify(ctx context.Context, opts ClassifyOpts) (*ClassifyResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if len(opts.Inputs) == 0 {
		return nil, ErrInputsRequired
	}

	call := provider.ClassifyCall{
		Inputs:          opts.Inputs,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	var onStart func(provider.ClassifyCall)
	if opts.OnClassifyStart != nil {
		onStart = func(c provider.ClassifyCall) { opts.OnClassifyStart(c.Inputs) }
	}

	resp, err := mediaCall(ctx, opts.MaxRetries, call, onStart, opts.Model.Classify, opts.OnClassifyEnd)
	if err != nil {
		return nil, err
	}
	if len(resp.Results) != len(opts.Inputs) {
		return nil, fmt.Errorf("ai: classification model returned %d results for %d inputs", len(resp.Results), len(opts.Inputs))
	}
	return &ClassifyResult{Results: resp.Results, Usage: resp.Usage}, nil
}
