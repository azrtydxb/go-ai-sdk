// Package gateway provides the Vercel AI Gateway provider: an
// OpenAI-compatible routing endpoint that fronts many upstream model
// providers (OpenAI, Anthropic, Google, and more) behind "provider/model"
// routing slugs such as "openai/gpt-4o" or "anthropic/claude-3-5-sonnet".
// Since the Gateway fronts heterogeneous upstreams, this package is a preset
// over the shared openaicompat base with NativeJSON left conservative
// (false) — the actual JSON-mode/schema support depends on which upstream
// model a given routing slug resolves to.
//
// Authentication uses a Gateway API key (env AI_GATEWAY_API_KEY) sent as a
// standard "Authorization: Bearer" header. Vercel also supports an OIDC
// token flow (VERCEL_OIDC_TOKEN, used implicitly inside Vercel deployments)
// as an alternative to a static API key; that path is out of scope for this
// package — only the AI_GATEWAY_API_KEY flow is supported here.
package gateway

import (
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://ai-gateway.vercel.sh/v1"

// Provider is a Vercel AI Gateway-backed provider.LanguageModel /
// EmbeddingModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key used for Authorization headers. Defaults to
// os.Getenv("AI_GATEWAY_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithBaseURL overrides the API base URL (default
// "https://ai-gateway.vercel.sh/v1").
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Vercel AI Gateway Provider.
func New(opts ...Option) *Provider {
	p := &Provider{apiKey: os.Getenv("AI_GATEWAY_API_KEY"), baseURL: defaultBaseURL, httpClient: http.DefaultClient}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Gateway routing slug
// (e.g. "openai/gpt-4o" or "anthropic/claude-3-5-sonnet"). The id is sent
// verbatim as the wire "model" field. NativeJSON is false since JSON-mode
// support varies by the upstream model the slug resolves to.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:       "gateway",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		NativeJSON: false,
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Gateway
// routing slug. EmbedBatch is conservatively 1 since batch-size limits vary
// by the upstream embedding model.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:       "gateway",
		APIKey:     p.apiKey,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
		EmbedBatch: 1,
	}, id)
}
