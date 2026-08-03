// Package fal implements the go-ai-sdk provider.ImageModel interface
// against fal.ai's synchronous fal.run image-generation endpoint.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package fal

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "fal"
	defaultBaseURL = "https://fal.run"
)

// Provider is a fal.ai-backed provider.ImageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Key <key>"
// header. Defaults to os.Getenv("FAL_API_KEY"), falling back to
// os.Getenv("FAL_KEY") when that's empty.
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default "https://fal.run").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests, including
// image URL downloads.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new fal.ai Provider.
func New(opts ...Option) *Provider {
	apiKey := os.Getenv("FAL_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("FAL_KEY")
	}
	p := &Provider{
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ImageModel returns a provider.ImageModel for the given fal.ai model ID
// (e.g. "fal-ai/flux/schnell").
func (p *Provider) ImageModel(id string) provider.ImageModel {
	return &imageModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// apiError converts a non-2xx HTTP response into an *ai.APICallError.
func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), string(body))
}

// ---- provider options ----

// applyProviderOptions merges providerOptions["fal"] (when it is a
// non-empty map[string]any) top-level into the already-marshaled JSON
// request object reqBytes, entries from the option map winning over
// whatever the SDK built. Returns reqBytes unchanged (no unmarshal/marshal
// round trip) when there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["fal"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, err
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}
