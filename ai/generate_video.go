package ai

import (
	"context"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// GenerateVideoOpts options for the GenerateVideo function.
type GenerateVideoOpts struct {
	Model           provider.VideoModel // required
	Prompt          string              // required
	AspectRatio     string
	Resolution      string
	DurationSec     float64
	MaxRetries      *int
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.VideoCall.Headers unchanged — see that field's
	// doc for precedence (it never overrides the provider's auth header)
	// and which request paths implement it.
	Headers map[string]string

	// OnVideoStart, when non-nil, fires once before the first attempt of
	// the underlying provider call (before job submission, for job-based
	// providers).
	OnVideoStart func(call provider.VideoCall)
	// OnVideoEnd, when non-nil, fires once after the final attempt resolves
	// (success or retry exhaustion; after the final poll, for job-based
	// providers). err, when non-nil, is the SAME error GenerateVideo itself
	// returns (retry exhaustion translated to *RetryError). resp is nil on
	// error.
	OnVideoEnd func(resp *provider.VideoResponse, err error)
}

// GenerateVideoResult is the outcome of a GenerateVideo call.
type GenerateVideoResult struct {
	Video  provider.GeneratedVideo // first video
	Videos []provider.GeneratedVideo
}

// GenerateVideo generates one or more videos from a text prompt using the
// provided model. It wraps the call in retry logic (default maxRetries = 2).
func GenerateVideo(ctx context.Context, opts GenerateVideoOpts) (*GenerateVideoResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if opts.Prompt == "" {
		return nil, ErrPromptRequired
	}

	call := provider.VideoCall{
		Prompt:          opts.Prompt,
		AspectRatio:     opts.AspectRatio,
		Resolution:      opts.Resolution,
		DurationSec:     opts.DurationSec,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	resp, err := mediaCall(ctx, opts.MaxRetries, call, opts.OnVideoStart, opts.Model.GenerateVideos, opts.OnVideoEnd)
	if err != nil {
		return nil, err
	}

	if len(resp.Videos) == 0 {
		return nil, fmt.Errorf("ai: video model returned no videos")
	}

	return &GenerateVideoResult{
		Video:  resp.Videos[0],
		Videos: resp.Videos,
	}, nil
}
