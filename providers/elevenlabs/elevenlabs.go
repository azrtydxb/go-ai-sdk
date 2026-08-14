// Package elevenlabs implements the go-ai-sdk provider.SpeechModel and
// provider.TranscriptionModel interfaces against the ElevenLabs API.
// ElevenLabs offers no language models, so this package exposes only
// speech synthesis and transcription.
package elevenlabs

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "elevenlabs"
	defaultBaseURL = "https://api.elevenlabs.io"
)

// Provider is an ElevenLabs-backed provider.SpeechModel /
// provider.TranscriptionModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the xi-api-key header. Defaults to
// os.Getenv("ELEVENLABS_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.elevenlabs.io").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new ElevenLabs Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("ELEVENLABS_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SpeechModel returns a provider.SpeechModel for the given ElevenLabs model
// ID (e.g. "eleven_multilingual_v2").
func (p *Provider) SpeechModel(id string) provider.SpeechModel {
	return &speechModel{provider: p, modelID: id}
}

// TranscriptionModel returns a provider.TranscriptionModel for the given
// ElevenLabs model ID (e.g. "scribe_v1").
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel {
	return &transcriptionModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// ---- shared error handling ----

// wireErrorDetail matches ElevenLabs' error body, which uses either a
// string or an object for the "detail" field.
type wireErrorDetail struct {
	Detail json.RawMessage `json:"detail"`
}

type wireErrorDetailObject struct {
	Message string `json:"message"`
}

// errorMessage tries to parse ElevenLabs' error body shapes:
// {"detail":{"message":...}} or {"detail":"..."}. Falls back to the raw
// body if parsing fails or no message field is present.
func errorMessage(body []byte) string {
	var we wireErrorDetail
	if err := json.Unmarshal(body, &we); err == nil && len(we.Detail) > 0 {
		var s string
		if err := json.Unmarshal(we.Detail, &s); err == nil && s != "" {
			return s
		}
		var obj wireErrorDetailObject
		if err := json.Unmarshal(we.Detail, &obj); err == nil && obj.Message != "" {
			return obj.Message
		}
	}
	return string(body)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}
