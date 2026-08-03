package provider

import (
	"context"
	"iter"
)

// StreamTranscriptionCall is the input to StreamingTranscriptionModel.StreamTranscribe.
type StreamTranscriptionCall struct {
	MediaType       string // e.g. "audio/pcm;rate=16000" — provider maps/validates
	Language        string
	SampleRate      int // hint for raw-PCM providers; 0 = provider default
	ProviderOptions map[string]any
}

// TranscriptEvent is one incremental transcription update delivered by a
// TranscriptionStream.
type TranscriptEvent struct {
	Text     string  // transcript delta or segment text
	Final    bool    // finalized segment vs interim hypothesis
	StartSec float64 // 0 when unknown
	EndSec   float64
}

// TranscriptionStream is a live bidirectional transcription session.
// One goroutine may Send audio while another ranges over Events.
type TranscriptionStream interface {
	Send(ctx context.Context, audio []byte) error
	// CloseSend signals end-of-audio; the provider flushes remaining
	// events and Events ends. Idempotent.
	CloseSend(ctx context.Context) error
	// Events yields transcript events until the stream ends. Single use.
	// After it ends, Err reports the terminal error, nil on clean end.
	Events() iter.Seq[TranscriptEvent]
	Err() error
	Close() error // aborts without flushing; idempotent
}

// StreamingTranscriptionModel opens live bidirectional transcription
// sessions.
type StreamingTranscriptionModel interface {
	StreamTranscribe(ctx context.Context, call StreamTranscriptionCall) (TranscriptionStream, error)
	ModelID() string
	ProviderName() string
}
