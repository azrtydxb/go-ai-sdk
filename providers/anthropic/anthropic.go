// Package anthropic implements the go-ai-sdk provider interfaces against
// Anthropic's Messages API.
//
// Anthropic has no embeddings API, so this package intentionally does not
// implement provider.EmbeddingModel — matching the TS AI SDK.
//
// # Extended thinking
//
// Extended thinking (Anthropic's "thinking" content blocks) is enabled per
// call via provider.Call.ProviderOptions, not a typed option — set
// ProviderOptions["anthropic"]["thinking"] to
// map[string]any{"type": "enabled", "budget_tokens": N}. When enabled, the
// response's thinking/redacted_thinking blocks surface as
// provider.ReasoningPart content parts (redacted_thinking sets
// ReasoningPart.Redacted and puts the opaque payload in Text; a regular
// thinking block sets Signature). Streaming surfaces the thinking text as
// provider.ReasoningDelta and the finished block (including any signature)
// as provider.ReasoningEnd. When an assistant message containing
// ReasoningParts is sent back to the API on a later turn, this package
// automatically reorders them to lead the message content, as the Messages
// API requires.
package anthropic

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	defaultMaxTokens = 4096
)

// Provider is an Anthropic-backed provider.LanguageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the x-api-key header. Defaults to
// os.Getenv("ANTHROPIC_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.anthropic.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Anthropic Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("ANTHROPIC_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Anthropic model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	return &languageModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}
