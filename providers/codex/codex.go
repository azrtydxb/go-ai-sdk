// Package codex implements the go-ai-sdk provider interfaces against
// the OpenAI Codex / ChatGPT Plus-Pro backend via OAuth authentication.
//
// This provider uses ChatGPT subscription credentials (OAuth tokens) rather
// than API keys, so billing is charged to the user's ChatGPT subscription.
package codex

import (
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/internal/codextransport"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// Provider is a Codex (ChatGPT Plus/Pro) LanguageModel factory.
type Provider struct {
	credential codexauth.Credential
	httpClient *http.Client
	baseURL    string
}

// Option configures a Provider.
type Option func(*Provider)

// WithCredentials sets the OAuth credentials for the provider.
func WithCredentials(cred codexauth.Credential) Option {
	return func(p *Provider) { p.credential = cred }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// WithBaseURL overrides the Codex backend URL (default
// "https://chatgpt.com/backend-api").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// New creates a new Codex Provider.
func New(opts ...Option) *Provider {
	p := &Provider{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Model returns a provider.LanguageModel for the given Codex model ID.
func (p *Provider) Model(id string) provider.LanguageModel {
	cfg := codextransport.Config{
		Credential: p.credential,
		ModelID:    id,
		BaseURL:    p.baseURL,
		HTTPClient: p.httpClient,
	}
	return codextransport.NewModelWithOptions(cfg)
}

// WithAPIKey is accepted for API compatibility but is a no-op for Codex.
// Codex uses OAuth subscription credentials, not API keys.
func WithAPIKey(k string) Option {
	return func(p *Provider) { _ = k }
}
