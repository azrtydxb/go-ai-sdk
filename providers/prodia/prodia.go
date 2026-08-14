// Package prodia implements the go-ai-sdk provider.ImageModel interface
// against Prodia's v2 synchronous inference API.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package prodia

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "prodia"
	defaultBaseURL = "https://inference.prodia.com/v2"
)

// Provider is a Prodia-backed provider.ImageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Bearer <key>"
// header. Defaults to os.Getenv("PRODIA_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://inference.prodia.com/v2").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Prodia Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("PRODIA_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ImageModel returns a provider.ImageModel for the given Prodia job type
// (e.g. "inference.flux.schnell.txt2img").
func (p *Provider) ImageModel(id string) provider.ImageModel {
	return &imageModel{provider: p, modelID: id}
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
