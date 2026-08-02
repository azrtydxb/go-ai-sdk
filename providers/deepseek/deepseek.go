// Package deepseek provides the Deepseek provider: Deepseek's API is
// OpenAI-chat-completions compatible, so this package is a preset over the
// shared openaicompat base.
package deepseek

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://api.deepseek.com/v1"

// Provider is a Deepseek-backed provider.LanguageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("DEEPSEEK_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://api.deepseek.com/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Deepseek Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("DEEPSEEK_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Deepseek model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "deepseek",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
		// DeepSeek documents "max_tokens", not OpenAI's current
		// "max_completion_tokens" — the latter is silently dropped.
		MaxTokensParam: "max_tokens",
		// DeepSeek's response_format only accepts json_object, not
		// json_schema; it rejects requests that send the latter.
		JSONObjectOnly: true,
	}, id)
}
