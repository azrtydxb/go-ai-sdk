// Package voyage implements the go-ai-sdk provider.EmbeddingModelWithOptions
// and provider.RerankingModel interfaces against Voyage AI's embeddings and
// rerank APIs.
package voyage

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL = "https://api.voyageai.com/v1"
	embeddingBatch = 128
	providerName   = "voyage"
)

// Provider is a Voyage-backed provider.EmbeddingModel / RerankingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the Authorization header. Defaults
// to os.Getenv("VOYAGE_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.voyageai.com/v1").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Voyage Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("VOYAGE_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// EmbeddingModel returns a provider.EmbeddingModel (also implementing
// provider.EmbeddingModelWithOptions) for the given Voyage embedding model
// ID (e.g. "voyage-3").
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return &embeddingModel{provider: p, modelID: id}
}

// RerankingModel returns a provider.RerankingModel for the given Voyage
// rerank model ID (e.g. "rerank-2").
func (p *Provider) RerankingModel(id string) provider.RerankingModel {
	return &rerankingModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}
