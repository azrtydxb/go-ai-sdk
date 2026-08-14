package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrPromptRequired is returned when Prompt is empty in GenerateImage options.
var ErrPromptRequired = errors.New("ai: prompt is required")

// GenerateImageOpts options for the GenerateImage function.
type GenerateImageOpts struct {
	Model           provider.ImageModel // required
	Prompt          string              // required
	N               int
	Size            string
	AspectRatio     string
	Seed            *int64
	MaxRetries      *int
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.ImageCall.Headers unchanged — see that field's
	// doc for precedence (it never overrides the provider's auth header)
	// and which request paths implement it.
	Headers map[string]string

	// OnImageStart, when non-nil, fires once before the first attempt of
	// the underlying provider call.
	OnImageStart func(call provider.ImageCall)
	// OnImageEnd, when non-nil, fires once after the final attempt (success
	// or retry exhaustion). err, when non-nil, is the SAME error
	// GenerateImage itself returns (retry exhaustion translated to
	// *RetryError). resp is nil on error.
	OnImageEnd func(resp *provider.ImageResponse, err error)
}

// GenerateImageResult is the outcome of a GenerateImage call.
type GenerateImageResult struct {
	Image  provider.GeneratedImage // first image
	Images []provider.GeneratedImage
}

// GenerateImage generates one or more images from a text prompt using the
// provided model. It wraps the call in retry logic (default maxRetries = 2).
func GenerateImage(ctx context.Context, opts GenerateImageOpts) (*GenerateImageResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if opts.Prompt == "" {
		return nil, ErrPromptRequired
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	call := provider.ImageCall{
		Prompt:          opts.Prompt,
		N:               opts.N,
		Size:            opts.Size,
		AspectRatio:     opts.AspectRatio,
		Seed:            opts.Seed,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	if opts.OnImageStart != nil {
		opts.OnImageStart(call)
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.ImageResponse, error) {
		return opts.Model.GenerateImages(ctx, call)
	})
	callErr := translateRetryErr(err)

	if opts.OnImageEnd != nil {
		opts.OnImageEnd(resp, callErr)
	}
	if callErr != nil {
		return nil, callErr
	}

	if len(resp.Images) == 0 {
		return nil, fmt.Errorf("ai: image model returned no images")
	}

	return &GenerateImageResult{
		Image:  resp.Images[0],
		Images: resp.Images,
	}, nil
}
