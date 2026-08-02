package provider

import "context"

// SpeechCall is the input to SpeechModel.GenerateSpeech.
type SpeechCall struct {
	Text            string
	Voice           string // provider-specific voice id; "" → provider default
	OutputFormat    string // e.g. "mp3", "wav"; "" → provider default
	Speed           *float64
	Language        string // BCP-47 hint where supported
	ProviderOptions map[string]any
}

// SpeechResponse is the outcome of a SpeechModel.GenerateSpeech call.
type SpeechResponse struct {
	Audio     []byte
	MediaType string // e.g. "audio/mpeg"
}

// SpeechModel synthesizes speech audio from text.
type SpeechModel interface {
	GenerateSpeech(ctx context.Context, call SpeechCall) (*SpeechResponse, error)
	ModelID() string
	ProviderName() string
}
