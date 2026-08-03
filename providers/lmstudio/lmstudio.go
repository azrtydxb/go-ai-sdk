// Package lmstudio provides the LM Studio provider: LM Studio's local
// server exposes an OpenAI-chat-completions compatible API, so this package
// is a preset over the shared openaicompat base. LM Studio is local-first:
// New defaults to no API key and a localhost base URL, and generally
// requires no authentication at all — LM Studio ignores the Authorization
// header it still receives.
package lmstudio

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "http://localhost:1234/v1"

// Provider is an LM Studio-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("LMSTUDIO_API_KEY"), which is empty for the common local
// case — LM Studio does not require authentication.
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "http://localhost:1234/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new LM Studio Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("LMSTUDIO_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given LM Studio model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "lmstudio",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: true,
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given LM Studio
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:       "lmstudio",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		EmbedBatch: 1,
	}, id)
}
