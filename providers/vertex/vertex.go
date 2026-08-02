// Package vertex implements the go-ai-sdk provider interfaces against
// Google Cloud's Vertex AI API (Gemini models hosted on Google Cloud).
// Language-model requests reuse internal/geminicompat, which speaks the
// same Gemini wire format Vertex exposes; embeddings use Vertex's own
// :predict wire format and are implemented directly in this package (see
// embedding.go).
package vertex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/gauth"
	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultLocation       = "us-central1"
	embeddingMaxBatchSize = 250
)

// Provider is a Vertex-AI-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	project     string
	location    string
	tokenSource gauth.TokenSource
	credErr     error // set when credential auto-discovery fails in New
	httpClient  *http.Client
	baseURL     string // override; empty -> derived from location
}

// Option configures a Provider.
type Option func(*Provider)

// WithProject sets the Google Cloud project ID. Defaults to
// os.Getenv("GOOGLE_VERTEX_PROJECT").
func WithProject(p string) Option { return func(pr *Provider) { pr.project = p } }

// WithLocation sets the Vertex AI location (region). Defaults to
// os.Getenv("GOOGLE_VERTEX_LOCATION"), or "us-central1" if that is unset.
func WithLocation(l string) Option { return func(pr *Provider) { pr.location = l } }

// WithTokenSource sets the gauth.TokenSource used to authorize requests,
// taking precedence over automatic GOOGLE_APPLICATION_CREDENTIALS
// discovery.
func WithTokenSource(ts gauth.TokenSource) Option {
	return func(pr *Provider) { pr.tokenSource = ts }
}

// WithAccessToken configures a fixed bearer token (wrapped in a
// gauth.StaticTokenSource), useful for tests or short-lived tokens
// obtained out of band.
func WithAccessToken(token string) Option {
	return func(pr *Provider) { pr.tokenSource = gauth.StaticTokenSource(token) }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(pr *Provider) { pr.httpClient = c } }

// WithBaseURL overrides the API base URL (default
// "https://{location}-aiplatform.googleapis.com/v1").
func WithBaseURL(u string) Option { return func(pr *Provider) { pr.baseURL = u } }

// New creates a new Vertex AI Provider. When no token source is configured
// via WithTokenSource/WithAccessToken, New checks
// GOOGLE_APPLICATION_CREDENTIALS and, if set, loads a service-account
// token source from that file. If that env var is unset too, requests
// against the returned Provider fail with "vertex: no credentials
// configured"; if it is set but the file cannot be loaded, requests fail
// with that load error instead.
func New(opts ...Option) *Provider {
	p := &Provider{
		project:    os.Getenv("GOOGLE_VERTEX_PROJECT"),
		location:   envOr("GOOGLE_VERTEX_LOCATION", defaultLocation),
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}

	if p.tokenSource == nil {
		if credPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credPath != "" {
			ts, err := gauth.NewServiceAccountTokenSourceFromFile(credPath)
			if err != nil {
				p.credErr = fmt.Errorf("vertex: load credentials from GOOGLE_APPLICATION_CREDENTIALS: %w", err)
			} else {
				p.tokenSource = ts
			}
		}
	}

	return p
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (p *Provider) resolvedBaseURL() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	return "https://" + p.location + "-aiplatform.googleapis.com/v1"
}

func (p *Provider) endpointFor(modelID, method string) string {
	return fmt.Sprintf("%s/projects/%s/locations/%s/publishers/google/models/%s:%s",
		p.resolvedBaseURL(), p.project, p.location, modelID, method)
}

func (p *Provider) authorize(ctx context.Context, req *http.Request) error {
	if p.credErr != nil {
		return p.credErr
	}
	if p.tokenSource == nil {
		return errors.New("vertex: no credentials configured")
	}
	tok, err := p.tokenSource.Token(ctx)
	if err != nil {
		return fmt.Errorf("vertex: mint access token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (p *Provider) config() geminicompat.Config {
	return geminicompat.Config{
		Name:        "vertex",
		HTTPClient:  p.httpClient,
		EndpointFor: p.endpointFor,
		Authorize:   p.authorize,
		EmbedBatch:  embeddingMaxBatchSize,
	}
}

// Model returns a provider.LanguageModel for the given Gemini model ID
// hosted on Vertex AI.
func (p *Provider) Model(id string) provider.LanguageModel {
	return geminicompat.NewLanguageModel(p.config(), id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Vertex AI
// text-embedding model ID.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return newEmbeddingModel(p, id)
}
