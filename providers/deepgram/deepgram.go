// Package deepgram implements the go-ai-sdk provider.TranscriptionModel
// interface against the Deepgram speech-to-text API.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package deepgram

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "deepgram"
	defaultBaseURL = "https://api.deepgram.com"
)

// Provider is a Deepgram-backed provider.TranscriptionModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Token <key>"
// header. Defaults to os.Getenv("DEEPGRAM_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.deepgram.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Deepgram Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("DEEPGRAM_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// TranscriptionModel returns a provider.TranscriptionModel for the given
// Deepgram model ID (e.g. "nova-3").
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel {
	return &transcriptionModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// apiError converts a non-2xx HTTP response into an *ai.APICallError.
func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

// wireError matches Deepgram's error body, {"err_msg": "..."}, with a
// fallback to a generic {"error": "..."} shape used by some endpoints.
type wireError struct {
	ErrMsg string `json:"err_msg"`
	Error  string `json:"error"`
}

// errorMessage tries to parse Deepgram's error body shapes:
// {"err_msg":"..."} or {"error":"..."}. Falls back to the raw body if
// parsing fails or no message field is present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil {
		if we.ErrMsg != "" {
			return we.ErrMsg
		}
		if we.Error != "" {
			return we.Error
		}
	}
	return string(body)
}
