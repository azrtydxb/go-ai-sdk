package provider

import (
	"context"
	"encoding/json"
)

// TranscriptionCall is the input to TranscriptionModel.Transcribe.
type TranscriptionCall struct {
	Audio           []byte
	MediaType       string // e.g. "audio/mpeg"; used for the upload filename/content-type
	Language        string // optional hint
	Prompt          string // optional context prompt
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
}

// TranscriptSegment is a single timed segment of a transcription.
type TranscriptSegment struct {
	Text     string
	StartSec float64
	EndSec   float64
}

// TranscriptionResponse is the outcome of a TranscriptionModel.Transcribe call.
type TranscriptionResponse struct {
	Text        string
	Segments    []TranscriptSegment // empty when the provider doesn't return segments
	Language    string              // detected language, "" if not reported
	DurationSec float64             // 0 if not reported
	Raw         json.RawMessage
}

// TranscriptionModel transcribes audio into text.
type TranscriptionModel interface {
	Transcribe(ctx context.Context, call TranscriptionCall) (*TranscriptionResponse, error)
	ModelID() string
	ProviderName() string
}
