// Package mistral implements the go-ai-sdk provider interfaces against
// Mistral's chat completions and embeddings API. Mistral's wire format is
// close to OpenAI's chat-completions shape but differs in enough details
// (tool_choice values, response_format, the max_tokens field name, and how
// stream usage is delivered) that this is a standalone implementation
// rather than a reuse of internal/openaicompat.
package mistral

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL = "https://api.mistral.ai/v1"
	embeddingBatch = 32
	providerName   = "mistral"
)

// Provider is a Mistral-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the Authorization header. Defaults
// to os.Getenv("MISTRAL_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.mistral.ai/v1").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Mistral Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("MISTRAL_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Mistral model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return &languageModel{provider: p, modelID: id}
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Mistral
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
