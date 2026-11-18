package ai

import (
	"context"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrListerRequired is returned when Lister is nil in ListModelsOpts.
var ErrListerRequired = errors.New("ai: lister is required")

// ErrCheckerRequired is returned when Checker is nil in CheckKeyOpts.
var ErrCheckerRequired = errors.New("ai: checker is required")

// ListModelsOpts options for the ListModels function.
type ListModelsOpts struct {
	Lister provider.ModelLister // required

	// OutputModality and InputModality filter the list; see
	// provider.ModelListCall for their exact semantics.
	OutputModality string
	InputModality  string

	MaxRetries *int

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.ModelListCall.Headers unchanged — see that field's
	// doc for precedence (it never overrides the provider's auth header).
	Headers map[string]string
}

// ListModels lists the models a provider offers, through its
// provider.ModelLister, in the order the provider returns them. It wraps the
// call in retry logic (default maxRetries = 2).
func ListModels(ctx context.Context, opts ListModelsOpts) ([]provider.ModelInfo, error) {
	if opts.Lister == nil {
		return nil, ErrListerRequired
	}
	call := provider.ModelListCall{
		OutputModality: opts.OutputModality,
		InputModality:  opts.InputModality,
		Headers:        opts.Headers,
	}
	return mediaCall(ctx, opts.MaxRetries, call, nil, opts.Lister.ListModels, nil)
}

// CheckKeyOpts options for the CheckKey function.
type CheckKeyOpts struct {
	Checker    provider.KeyChecker // required
	MaxRetries *int

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.KeyCheckCall.Headers unchanged.
	Headers map[string]string
}

// CheckKey validates the API key a provider was configured with, through
// its provider.KeyChecker. A nil error means the key was accepted; a
// rejected key is an *APICallError with StatusCode 401 or 403. It wraps the
// call in retry logic (default maxRetries = 2); 401 and 403 are not retried.
func CheckKey(ctx context.Context, opts CheckKeyOpts) (*provider.KeyInfo, error) {
	if opts.Checker == nil {
		return nil, ErrCheckerRequired
	}
	call := provider.KeyCheckCall{Headers: opts.Headers}
	return mediaCall(ctx, opts.MaxRetries, call, nil, opts.Checker.CheckKey, nil)
}
