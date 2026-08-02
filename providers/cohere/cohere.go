// Package cohere implements the go-ai-sdk provider interfaces against
// Cohere's v2 chat and embed APIs. Cohere's wire format differs enough from
// OpenAI-compatible chat-completions shapes (typed SSE payloads dispatched
// on a "type" field, a "p" field for top_p, no tool_choice field, and a
// distinct embed endpoint/response shape) that this is a standalone
// implementation.
package cohere

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL = "https://api.cohere.com/v2"
	embeddingBatch = 96
	providerName   = "cohere"
)

// Provider is a Cohere-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the Authorization header. Defaults
// to os.Getenv("COHERE_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.cohere.com/v2").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Cohere Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("COHERE_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Cohere model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return &languageModel{provider: p, modelID: id}
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Cohere
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return &embeddingModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}
