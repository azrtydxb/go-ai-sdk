// Package nvidia provides the NVIDIA NIM provider: NVIDIA's NIM API
// endpoints are OpenAI-chat-completions compatible, so this package is a
// preset over the shared openaicompat base.
package nvidia

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://integrate.api.nvidia.com/v1"

// Provider is an NVIDIA NIM-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("NVIDIA_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://integrate.api.nvidia.com/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new NVIDIA NIM Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("NVIDIA_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given NVIDIA NIM model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "nvidia",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		// NVIDIA NIM fronts many different underlying models across a
		// multi-tenant catalog, so NativeJSON is optimistic here (mirrors
		// huggingface/gateway's rationale for their own JSON-mode
		// defaults): callers whose model rejects json_schema can drop to
		// json-object mode via ProviderOptions.
		NativeJSON: true,
		// NVIDIA NIM documents "max_tokens", not OpenAI's current
		// "max_completion_tokens" — the latter is silently dropped.
		MaxTokensParam: "max_tokens",
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given NVIDIA NIM
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:       "nvidia",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		EmbedBatch: 1,
	}, id)
}
