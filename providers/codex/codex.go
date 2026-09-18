// Package codex implements the go-ai-sdk provider interfaces against
// the OpenAI Codex / ChatGPT Plus-Pro backend via OAuth authentication.
//
// This provider uses ChatGPT subscription credentials (OAuth tokens) rather
// than API keys, so billing is charged to the user's ChatGPT subscription.
package codex

import (
	"context"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/internal/codextransport"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// Credential holds Codex OAuth tokens, expiry, and the ChatGPT account ID.
type Credential = codexauth.Credential

// Provider is a Codex (ChatGPT Plus/Pro) LanguageModel factory.
type Provider struct {
	credential       codexauth.Credential
	credentialSource func(context.Context) (Credential, error)
	httpClient       *http.Client
	baseURL          string
}

// Option configures a Provider.
type Option func(*Provider)

// WithCredentials sets the OAuth credentials for the provider.
func WithCredentials(cred Credential) Option {
	return func(p *Provider) { p.credential = cred }
}

// WithCredentialSource resolves credentials on every Generate and Stream call.
// A non-nil source takes precedence over WithCredentials. The caller owns
// refresh, secure persistence, and synchronization of concurrent source calls.
func WithCredentialSource(source func(context.Context) (Credential, error)) Option {
	return func(p *Provider) { p.credentialSource = source }
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
	model := codextransport.NewModelWithOptions(cfg)
	if p.credentialSource != nil {
		return &credentialModel{LanguageModel: model, config: cfg, source: p.credentialSource}
	}
	return model
}

type credentialModel struct {
	provider.LanguageModel
	config codextransport.Config
	source func(context.Context) (Credential, error)
}

func (m *credentialModel) resolve(ctx context.Context) (provider.LanguageModel, error) {
	credential, err := m.source(ctx)
	if err != nil {
		return nil, err
	}
	cfg := m.config
	cfg.Credential = credential
	return codextransport.NewModelWithOptions(cfg), nil
}

func (m *credentialModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	model, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return model.Generate(ctx, call)
}

func (m *credentialModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	model, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return model.Stream(ctx, call)
}

// WithAPIKey is accepted for API compatibility but is a no-op for Codex.
// Codex uses OAuth subscription credentials, not API keys.
func WithAPIKey(k string) Option {
	return func(p *Provider) { _ = k }
}
