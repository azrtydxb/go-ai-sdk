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

type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

type Option func(*Provider)

func WithAPIKey(k string) Option           { return func(p *Provider) { p.apiKey = k } }
func WithBaseURL(u string) Option          { return func(p *Provider) { p.baseURL = u } }
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("PERPLEXITY_API_KEY"), baseURL: defaultBaseURL}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "perplexity",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
	}, id)
}
