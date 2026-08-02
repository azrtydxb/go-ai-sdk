package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrAudioRequired is returned when Audio is empty in Transcribe options.
var ErrAudioRequired = errors.New("ai: audio is required")

// TranscribeOpts options for the Transcribe function.
type TranscribeOpts struct {
	Model           provider.TranscriptionModel
	Audio           []byte
	MediaType       string
	Language        string
	Prompt          string
	MaxRetries      *int
	ProviderOptions map[string]any
}

// TranscribeResult is the outcome of a Transcribe call.
type TranscribeResult struct {
	Text        string
	Segments    []provider.TranscriptSegment
	Language    string
	DurationSec float64
}

// Transcribe transcribes audio into text using the provided model. It wraps
// the call in retry logic (default maxRetries = 2).
func Transcribe(ctx context.Context, opts TranscribeOpts) (*TranscribeResult, error) {
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

	call := provider.TranscriptionCall{
		Audio:           opts.Audio,
		MediaType:       opts.MediaType,
		Language:        opts.Language,
		Prompt:          opts.Prompt,
		ProviderOptions: opts.ProviderOptions,
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.TranscriptionResponse, error) {
		return opts.Model.Transcribe(ctx, call)
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
		return nil, err
	}

	if resp.Text == "" {
		return nil, fmt.Errorf("ai: transcription model returned no text")
	}

	return &TranscribeResult{
		Text:        resp.Text,
		Segments:    resp.Segments,
		Language:    resp.Language,
		DurationSec: resp.DurationSec,
	}, nil
}
