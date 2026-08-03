// Package baseten provides the Baseten provider: Baseten's Model APIs
// expose an OpenAI-chat-completions compatible interface, so this package is
// a preset over the shared openaicompat base.
package baseten

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://inference.baseten.co/v1"

// Provider is a Baseten-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("BASETEN_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://inference.baseten.co/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Baseten Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("BASETEN_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Baseten model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "baseten",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Baseten
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:       "baseten",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		EmbedBatch: 1,
	}, id)
}
