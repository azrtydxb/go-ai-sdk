// Package replicate implements the go-ai-sdk provider.ImageModel interface
// against Replicate's synchronous ("Prefer: wait") predictions API.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package replicate

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "replicate"
	defaultBaseURL = "https://api.replicate.com"
)

// Provider is a Replicate-backed provider.ImageModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Bearer <key>"
// header. Defaults to os.Getenv("REPLICATE_API_TOKEN").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.replicate.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests, including
// image URL downloads.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Replicate Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("REPLICATE_API_TOKEN"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ImageModel returns a provider.ImageModel for the given Replicate model ID
// (e.g. "black-forest-labs/flux-schnell").
func (p *Provider) ImageModel(id string) provider.ImageModel {
	return &imageModel{provider: p, modelID: id}
}

// VideoModel returns a provider.VideoModel for the given Replicate model ID
// (e.g. "minimax/video-01").
func (p *Provider) VideoModel(id string) provider.VideoModel {
	return &videoModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// apiError converts a non-2xx HTTP response into an *ai.APICallError.
func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

// wireError matches Replicate's error body shapes: either a simple
// {"detail":"..."} (e.g. auth errors) or an RFC-7807-style problem object
// carrying "title" and/or "detail".
type wireError struct {
	Detail string `json:"detail"`
	Title  string `json:"title"`
}

// errorMessage tries to parse Replicate's error body shapes, preferring
// "detail" then falling back to "title". Falls back to the raw body if
// parsing fails or neither field is present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil {
		if we.Detail != "" {
			return we.Detail
		}
		if we.Title != "" {
			return we.Title
		}
	}
	return string(body)
}

// ---- provider options ----

// applyProviderOptions merges providerOptions["replicate"] (when it is a
// non-empty map[string]any) into the request's "input" object, entries
// from the option map winning over whatever the SDK built.
//
// This diverges from most other providers' ProviderOptions convention
// (which merge top-level into the request body): Replicate's predictions
// API nests all model parameters under "input", so replicate provider
// options are treated as additional/overriding model inputs rather than
// top-level request fields.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["replicate"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, err
	}
	input, _ := m["input"].(map[string]any)
	if input == nil {
		input = map[string]any{}
	}
	for k, v := range opts {
		input[k] = v
	}
	m["input"] = input
	return json.Marshal(m)
}
