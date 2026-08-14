package provider

import (
	"context"
	"encoding/json"
)

// TranslationCall is the input to TranslationModel.Translate.
type TranslationCall struct {
	Audio           []byte
	MediaType       string // e.g. "audio/mpeg"; used for the upload filename/content-type
	Prompt          string // optional context prompt
	ProviderOptions map[string]any

	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
}

// TranslationResponse is the outcome of a TranslationModel.Translate call.
// Unlike TranscriptionModel, TranslationModel always produces English text
// regardless of the source audio's language.
type TranslationResponse struct {
	Text        string  // English translation
	Language    string  // detected source language, "" if not reported
	DurationSec float64 // 0 if not reported
	Raw         json.RawMessage
}

// TranslationModel translates audio in any supported source language into
// English text.
//
// TranslationModel is not wired into ai.Registry this wave (niche
// modality) — construct provider-specific instances directly (e.g.
// openai.Provider.TranslationModel) and pass them to ai.Translate.
type TranslationModel interface {
	Translate(ctx context.Context, call TranslationCall) (*TranslationResponse, error)
	ModelID() string
	ProviderName() string
}
