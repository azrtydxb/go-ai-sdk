// Package xai provides the X.AI provider: X.AI's API is
// OpenAI-chat-completions compatible, so this package is a preset over the
// shared openaicompat base.
package xai

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://api.x.ai/v1"

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
	p := &Provider{apiKey: os.Getenv("XAI_API_KEY"), baseURL: defaultBaseURL}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "xai",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
	}, id)
}
