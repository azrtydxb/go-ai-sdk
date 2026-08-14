// Package mistral implements the go-ai-sdk provider interfaces against
// Mistral's chat completions and embeddings API, as a preset over the
// shared openaicompat base. Mistral's wire divergences from OpenAI's shape
// (max_tokens/random_seed field names, tool_choice "any", tools omitted on
// ToolChoiceNone, json_object-only response_format, usage on the final
// stream chunk instead of stream_options) are expressed as openaicompat
// Config knobs — see (*Provider).config.
package mistral

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL = "https://api.mistral.ai/v1"
	embeddingBatch = 32
	providerName   = "mistral"
)

// Provider is a Mistral-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the Authorization header. Defaults
// to os.Getenv("MISTRAL_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.mistral.ai/v1").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Mistral Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("MISTRAL_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// config maps the Provider onto the openaicompat base, with Mistral's wire
// divergences expressed as Config knobs: max_tokens/random_seed field
// names, tool_choice "any" for required, tools omitted entirely on
// ToolChoiceNone, json_object-only response_format, and no stream_options
// (usage arrives on the final content chunk instead).
func (p *Provider) config() openaicompat.Config {
	return openaicompat.Config{
		Name:               providerName,
		APIKey:             p.apiKey,
		BaseURL:            p.baseURL,
		HTTPClient:         p.httpClient,
		NativeJSON:         true,
		EmbedBatch:         embeddingBatch,
		MaxTokensParam:     "max_tokens",
		JSONObjectOnly:     true,
		SeedParam:          "random_seed",
		RequiredToolChoice: "any",
		OmitToolsOnNone:    true,
		NoStreamOptions:    true,
		NoReasoningEffort:  true,
	}
}

// Model returns a provider.LanguageModel for the given Mistral model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(p.config(), id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Mistral
// embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(p.config(), id)
}
