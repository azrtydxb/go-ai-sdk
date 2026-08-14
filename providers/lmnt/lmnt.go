// Package lmnt implements the go-ai-sdk provider.SpeechModel interface
// against LMNT's text-to-speech API.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package lmnt

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "lmnt"
	defaultBaseURL = "https://api.lmnt.com"
)

// Provider is an LMNT-backed provider.SpeechModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the X-API-Key header. Defaults to
// os.Getenv("LMNT_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default "https://api.lmnt.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new LMNT Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("LMNT_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SpeechModel returns a provider.SpeechModel for the given LMNT model ID
// (e.g. "blizzard").
func (p *Provider) SpeechModel(id string) provider.SpeechModel {
	return &speechModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), providerutil.ErrorMessage(body))
}
