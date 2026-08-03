// Package qwen provides the Qwen provider: Alibaba's DashScope
// OpenAI-compatible mode is OpenAI-chat-completions compatible, so this
// package is a preset over the shared openaicompat base.
package qwen

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"

// Provider is a Qwen-backed provider.LanguageModel / EmbeddingModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("DASHSCOPE_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://dashscope-intl.aliyuncs.com/compatible-mode/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Qwen Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("DASHSCOPE_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Qwen model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "qwen",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		// DashScope's OpenAI-compatible mode fronts many different
		// underlying Qwen model variants across a multi-tenant catalog, so
		// NativeJSON is optimistic here (mirrors huggingface/gateway's
		// rationale for their own JSON-mode defaults): callers whose model
		// rejects json_schema can drop to json-object mode via
		// ProviderOptions.
		NativeJSON: true,
		// DashScope's OpenAI-compatible mode documents "max_tokens", not
		// OpenAI's current "max_completion_tokens".
		MaxTokensParam: "max_tokens",
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Qwen
// embedding model ID, e.g. "text-embedding-v3".
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:       "qwen",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		EmbedBatch: 10,
	}, id)
}
