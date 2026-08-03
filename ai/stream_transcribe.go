package ai

import (
	"context"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// StreamTranscribeOpts are the options for StreamTranscribe.
type StreamTranscribeOpts struct {
	Model           provider.StreamingTranscriptionModel
	MediaType       string
	Language        string
	SampleRate      int
	ProviderOptions map[string]any
}

// StreamTranscribe opens a live bidirectional transcription session against
// opts.Model. Unlike Transcribe, there is no retry: a live connection
// failing mid-stream cannot be transparently retried.
func StreamTranscribe(ctx context.Context, opts StreamTranscribeOpts) (provider.TranscriptionStream, error) {
	if opts.Model == nil {
		return nil, ErrModelRequired
	}

	call := provider.StreamTranscriptionCall{
		MediaType:       opts.MediaType,
		Language:        opts.Language,
		SampleRate:      opts.SampleRate,
		ProviderOptions: opts.ProviderOptions,
	}

	return opts.Model.StreamTranscribe(ctx, call)
}
