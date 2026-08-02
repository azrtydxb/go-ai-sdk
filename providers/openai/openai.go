// Package openai implements the go-ai-sdk provider interfaces against
// OpenAI's Chat Completions and Embeddings APIs. It is a thin preset over
// the shared internal/openaicompat implementation.
package openai

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL        = "https://api.openai.com/v1"
	embeddingMaxBatchSize = 2048
)

// Provider is an OpenAI-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("OPENAI_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.openai.com/v1").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new OpenAI Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("OPENAI_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Provider) config() openaicompat.Config {
	return openaicompat.Config{
		Name:       "openai",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
		EmbedBatch: embeddingMaxBatchSize,
	}
}

// Model returns a provider.LanguageModel for the given OpenAI model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(p.config(), id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given OpenAI
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(p.config(), id)
}
