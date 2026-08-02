// Package perplexity provides the Perplexity provider: Perplexity's API is
// OpenAI-chat-completions compatible, so this package is a preset over the
// shared openaicompat base.
// Perplexity's API does not support tool calling; Tools in a Call are serialized but the live API may reject or ignore them.
package perplexity

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://api.perplexity.ai"

// Provider is a Perplexity-backed provider.LanguageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("PERPLEXITY_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://api.perplexity.ai").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Perplexity Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("PERPLEXITY_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Perplexity model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "perplexity",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
		// Perplexity documents "max_tokens", not OpenAI's current
		// "max_completion_tokens" — the latter is silently dropped.
		MaxTokensParam: "max_tokens",
	}, id)
}
