package ai

import (
	"context"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
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

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	call := provider.VideoCall{
		Prompt:          opts.Prompt,
		AspectRatio:     opts.AspectRatio,
		Resolution:      opts.Resolution,
		DurationSec:     opts.DurationSec,
		ProviderOptions: opts.ProviderOptions,
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.VideoResponse, error) {
		return opts.Model.GenerateVideos(ctx, call)
	})
	if err != nil {
		return nil, translateRetryErr(err)
	}

	if len(resp.Videos) == 0 {
		return nil, fmt.Errorf("ai: video model returned no videos")
	}

	return &GenerateVideoResult{
		Video:  resp.Videos[0],
		Videos: resp.Videos,
	}, nil
}
