// Package google implements the go-ai-sdk provider interfaces against
// Google's Generative Language API (Gemini). It is a thin preset over the
// shared internal/geminicompat implementation.
package google

import (
	"context"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL        = "https://generativelanguage.googleapis.com/v1beta"
	embeddingMaxBatchSize = 100
)

// Provider is a Google-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the x-goog-api-key header. Defaults
// to os.Getenv("GOOGLE_GENERATIVE_AI_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://generativelanguage.googleapis.com/v1beta").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Google Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("GOOGLE_GENERATIVE_AI_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *Provider) endpointFor(modelID, method string) string {
	return p.baseURL + "/models/" + modelID + ":" + method
}

func (p *Provider) authorize(_ context.Context, req *http.Request) error {
	req.Header.Set("x-goog-api-key", p.apiKey)
	return nil
}

func (p *Provider) config() geminicompat.Config {
	return geminicompat.Config{
		Name:        "google",
		HTTPClient:  p.httpClient,
		EndpointFor: p.endpointFor,
		Authorize:   p.authorize,
		EmbedBatch:  embeddingMaxBatchSize,
	}
}

// Model returns a provider.LanguageModel for the given Gemini model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return geminicompat.NewLanguageModel(p.config(), id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Gemini
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return geminicompat.NewEmbeddingModel(p.config(), id)
}
