package provider

import (
	"context"
	"encoding/json"
	"time"
)

// ModelListCall is the input to ModelLister.ListModels.
type ModelListCall struct {
	// OutputModality keeps only models that can produce this modality, e.g.
	// "text", "image", "speech", "audio", "embeddings". Where the provider
	// filters server-side (OpenRouter's output_modalities query parameter)
	// it is sent there too. A model that reports no output modalities is
	// kept, since nothing says it cannot. "" lists the provider's default
	// set; on OpenRouter that is text-output models only, and "all" lists
	// every model.
	OutputModality string

	// InputModality keeps only models that accept this modality, e.g.
	// "text", "image", "audio", "file". Applied client-side; a model that
	// reports no input modalities is kept.
	InputModality string

	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
}

// ModelInfo describes one model a provider offers. Fields the provider does
// not report are left at their zero value.
type ModelInfo struct {
	ID          string
	Name        string // human-readable name, e.g. "ElevenLabs: Eleven v3"
	Description string
	OwnedBy     string    // OpenAI's owned_by
	Created     time.Time // zero when not reported

	// ContextLength is the context window in tokens; 0 when not reported.
	ContextLength int

	// InputModalities and OutputModalities are what the model accepts and
	// produces, as the provider names them (OpenRouter:
	// architecture.input_modalities / architecture.output_modalities).
	InputModalities  []string
	OutputModalities []string

	// Voices are the voice ids a speech model accepts as SpeechCall.Voice,
	// when the provider lists them (OpenRouter: supported_voices).
	Voices []string

	// Raw is the provider's JSON object for this model, for fields ModelInfo
	// doesn't surface (pricing, supported parameters, ...).
	Raw json.RawMessage
}

// ModelLister lists the models a provider offers. It is a provider-level
// capability, like FileStore, obtained from the provider package (for
// example openaicompatible's (*Provider).ModelLister).
type ModelLister interface {
	ListModels(ctx context.Context, call ModelListCall) ([]ModelInfo, error)
	ProviderName() string
}

// KeyCheckCall is the input to KeyChecker.CheckKey.
type KeyCheckCall struct {
	// Headers carries extra HTTP headers applied to the request(s) this call
	// makes, after auth; a key matching the provider's auth header is
	// ignored. Same contract as Call.Headers.
	Headers map[string]string
}

// KeyInfo is what a provider reports about the API key it was configured
// with. A provider that can only confirm the key works (by making an
// authenticated request) returns a KeyInfo with Detailed false and nothing
// else set.
type KeyInfo struct {
	// Detailed reports whether the provider has a key-info endpoint and the
	// fields below come from it. When false the key was validated by an
	// authenticated request and nothing else is known.
	Detailed bool

	Label string // the key's name or masked form, e.g. OpenRouter's label

	// Limit is the key's credit limit, nil when unlimited or not reported.
	Limit *float64
	// Usage is the credit the key has used, nil when not reported.
	Usage *float64
	// LimitRemaining is the credit left under Limit, nil when unlimited or
	// not reported.
	LimitRemaining *float64
	// FreeTier reports whether the key belongs to a free-tier account.
	FreeTier bool

	// Raw is the provider's raw JSON key-info response, for fields KeyInfo
	// doesn't surface (rate limits, daily usage, ...). Nil when Detailed is
	// false.
	Raw json.RawMessage
}

// KeyChecker validates the API key a provider was configured with. CheckKey
// returns a nil error when the key is accepted; a rejected key is reported
// the way every other call reports it, as an *ai.APICallError (401 or 403).
type KeyChecker interface {
	CheckKey(ctx context.Context, call KeyCheckCall) (*KeyInfo, error)
	ProviderName() string
}
