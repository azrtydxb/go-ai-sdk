// Package anthropic implements the go-ai-sdk provider interfaces against
// Anthropic's Messages API.
//
// Anthropic has no embeddings API, so this package intentionally does not
// implement provider.EmbeddingModel — matching the TS AI SDK.
package anthropic

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 4096
)

// Provider is an Anthropic-backed provider.LanguageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the x-api-key header. Defaults to
// os.Getenv("ANTHROPIC_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.anthropic.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Anthropic Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("ANTHROPIC_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Anthropic model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return &languageModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}
