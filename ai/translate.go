package ai

import (
	"context"

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

	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.TranslationCall.Headers unchanged — see that
	// field's doc for precedence (it never overrides the provider's auth
	// header) and which request paths implement it.
	Headers map[string]string

	// OnTranslateStart, when non-nil, fires once before the first attempt of
	// the underlying provider call.
	OnTranslateStart func(call provider.TranslationCall)
	// OnTranslateEnd, when non-nil, fires once after the final attempt
	// (success or retry exhaustion). err, when non-nil, is the SAME error
	// Translate itself returns (retry exhaustion translated to
	// *RetryError). resp is nil on error.
	OnTranslateEnd func(resp *provider.TranslationResponse, err error)
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

	call := provider.TranslationCall{
		Audio:           opts.Audio,
		MediaType:       opts.MediaType,
		Prompt:          opts.Prompt,
		ProviderOptions: opts.ProviderOptions,
		Headers:         opts.Headers,
	}

	resp, err := mediaCall(ctx, opts.MaxRetries, call, opts.OnTranslateStart, opts.Model.Translate, opts.OnTranslateEnd)
	if err != nil {
		return nil, err
	}

	return &TranslateResult{
		Text:        resp.Text,
		Language:    resp.Language,
		DurationSec: resp.DurationSec,
	}, nil
}
