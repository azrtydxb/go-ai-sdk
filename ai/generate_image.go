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

// mediaCall is the shared skeleton behind the media wrappers (GenerateImage,
// GenerateSpeech, GenerateVideo, Transcribe, Translate, Rerank, UploadFile,
// DeleteFile): fire the optional start hook with the built call, run do under
// retry (nil maxRetries → defaultMaxRetries), translate retry exhaustion to
// *RetryError, then fire the optional end hook with the SAME error the caller
// gets (resp is the zero value on error).
func mediaCall[C, R any](ctx context.Context, maxRetries *int, call C, onStart func(C), do func(context.Context, C) (R, error), onEnd func(R, error)) (R, error) {
	mr := defaultMaxRetries
	if maxRetries != nil {
		mr = *maxRetries
	}

	if onStart != nil {
		onStart(call)
	}

	resp, err := retry.Do(ctx, mr, func() (R, error) {
		return do(ctx, call)
	})
	callErr := translateRetryErr(err)

	if onEnd != nil {
		onEnd(resp, callErr)
	}
	if callErr != nil {
		var zero R
		return zero, callErr
	}
	return resp, nil
}

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

	call := provider.ImageCall{
		Prompt:          opts.Prompt,
		N:               opts.N,
		Size:            opts.Size,
		AspectRatio:     opts.AspectRatio,
		Seed:            opts.Seed,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	resp, err := mediaCall(ctx, opts.MaxRetries, call, opts.OnImageStart, opts.Model.GenerateImages, opts.OnImageEnd)
	if err != nil {
		return nil, err
	}

	if len(resp.Images) == 0 {
		return nil, fmt.Errorf("ai: image model returned no images")
	}

	return &GenerateImageResult{
		Image:  resp.Images[0],
		Images: resp.Images,
	}, nil
}
