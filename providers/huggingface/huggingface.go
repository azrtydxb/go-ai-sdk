// Package huggingface provides the Hugging Face provider: Hugging Face's
// Inference Router exposes an OpenAI-chat-completions compatible API, so
// this package is a preset over the shared openaicompat base.
package huggingface

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://router.huggingface.co/v1"

// Provider is a Hugging Face-backed provider.LanguageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("HF_TOKEN").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://router.huggingface.co/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Hugging Face Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("HF_TOKEN"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Hugging Face model
// ID, e.g. "meta-llama/Meta-Llama-3-8B-Instruct:together".
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "huggingface",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		// The router fans out to many different underlying inference
		// providers with varying JSON-mode support, so this preset is
		// conservative and does not advertise native JSON support.
		NativeJSON: false,
		// The router documents "max_tokens", not OpenAI's current
		// "max_completion_tokens".
		MaxTokensParam: "max_tokens",
	}, id)
}
