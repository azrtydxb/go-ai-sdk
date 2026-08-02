package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrTextRequired is returned when Text is empty in GenerateSpeech options.
var ErrTextRequired = errors.New("ai: text is required")

// GenerateSpeechOpts options for the GenerateSpeech function.
type GenerateSpeechOpts struct {
	Model           provider.SpeechModel
	Text            string
	Voice           string
	OutputFormat    string
	Speed           *float64
	Language        string
	MaxRetries      *int
	ProviderOptions map[string]any
}

// GenerateSpeechResult is the outcome of a GenerateSpeech call.
type GenerateSpeechResult struct {
	Audio     []byte
	MediaType string
}

// GenerateSpeech synthesizes speech audio from text using the provided
// model. It wraps the call in retry logic (default maxRetries = 2).
func GenerateSpeech(ctx context.Context, opts GenerateSpeechOpts) (*GenerateSpeechResult, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if opts.Text == "" {
		return nil, ErrTextRequired
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	call := provider.SpeechCall{
		Text:            opts.Text,
		Voice:           opts.Voice,
		OutputFormat:    opts.OutputFormat,
		Speed:           opts.Speed,
		Language:        opts.Language,
		ProviderOptions: opts.ProviderOptions,
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.SpeechResponse, error) {
		return opts.Model.GenerateSpeech(ctx, call)
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
		return nil, err
	}

	if len(resp.Audio) == 0 {
		return nil, fmt.Errorf("ai: speech model returned no audio")
	}

	return &GenerateSpeechResult{
		Audio:     resp.Audio,
		MediaType: resp.MediaType,
	}, nil
}
