// Package hume implements the go-ai-sdk provider.SpeechModel interface
// against Hume's Octave text-to-speech API.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package hume

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName   = "hume"
	defaultBaseURL = "https://api.hume.ai"
)

// Provider is a Hume-backed provider.SpeechModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the X-Hume-Api-Key header. Defaults
// to os.Getenv("HUME_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default "https://api.hume.ai").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Hume Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("HUME_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// SpeechModel returns a provider.SpeechModel for the given Hume model ID.
//
// Note: Hume's /v0/tts endpoint does not currently accept a model
// selector in its request body, so the model ID is accepted here only for
// interface symmetry with other providers and has no effect on the wire
// request.
func (p *Provider) SpeechModel(id string) provider.SpeechModel {
	return &speechModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// ---- shared error handling ----

// wireError matches Hume's error body shapes: {"message":"..."} or
// {"error":"..."}.
type wireError struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

// errorMessage tries to parse Hume's error body shapes: {"message":"..."}
// or {"error":"..."}. Falls back to the raw body if parsing fails or no
// message is present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil {
		if we.Message != "" {
			return we.Message
		}
		if we.Error != "" {
			return we.Error
		}
	}
	return string(body)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

// ---- provider options ----

// applyProviderOptions merges providerOptions["hume"] (when it is a
// non-empty map[string]any) top-level into the already-marshaled JSON
// request object reqBytes, entries from the option map winning over
// whatever the SDK built. Returns reqBytes unchanged (no unmarshal/marshal
// round trip) when there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["hume"].(map[string]any)
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
