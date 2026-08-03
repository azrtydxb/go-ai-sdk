package ai

import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TranslateOpts options for the Translate function.
type TranslateOpts struct {
	Model           provider.TranslationModel
	Audio           []byte
	MediaType       string
	Prompt          string
	MaxRetries      *int
	ProviderOptions map[string]any
}

// TranslateResult is the outcome of a Translate call.
type TranslateResult struct {
	Text        string // English translation
	Language    string // detected source language, "" if not reported
	DurationSec float64
}

// Translate translates audio in any supported source language into English
// text using the provided model. It wraps the call in retry logic (default
// maxRetries = 2).
func Translate(ctx context.Context, opts TranslateOpts) (*TranslateResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if len(opts.Audio) == 0 {
		return nil, ErrAudioRequired
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	call := provider.TranslationCall{
		Audio:           opts.Audio,
		MediaType:       opts.MediaType,
		Prompt:          opts.Prompt,
		ProviderOptions: opts.ProviderOptions,
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.TranslationResponse, error) {
		return opts.Model.Translate(ctx, call)
	})
	if err != nil {
		return nil, translateRetryErr(err)
	}

	return &TranslateResult{
		Text:        resp.Text,
		Language:    resp.Language,
		DurationSec: resp.DurationSec,
	}, nil
}
