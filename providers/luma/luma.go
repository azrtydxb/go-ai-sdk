// Package luma implements the go-ai-sdk provider.ImageModel interface
// against Luma's Dream Machine image-generation API, which is asynchronous:
// a generation is created, then polled until it reaches a terminal state.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package luma

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName        = "luma"
	defaultBaseURL      = "https://api.lumalabs.ai"
	defaultPollInterval = 500 * time.Millisecond
)

// Provider is a Luma Dream Machine-backed provider.ImageModel factory.
type Provider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Bearer <key>"
// header. Defaults to os.Getenv("LUMA_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.lumalabs.ai").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests, including
// image URL downloads.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// WithPollInterval overrides the interval between generation status polls
// (default 500ms). Primarily a test hook so fixtures can poll fast.
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) { p.pollInterval = d }
}

// New creates a new Luma Dream Machine Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:       os.Getenv("LUMA_API_KEY"),
		baseURL:      defaultBaseURL,
		httpClient:   http.DefaultClient,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ImageModel returns a provider.ImageModel for the given Luma model ID
// (e.g. "photon-1").
func (p *Provider) ImageModel(id string) provider.ImageModel {
	return &imageModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

func (p *Provider) poll() time.Duration {
	if p.pollInterval > 0 {
		return p.pollInterval
	}
	return defaultPollInterval
}

// apiError converts a non-2xx HTTP response into an *ai.APICallError.
func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

// wireErrorDetail matches Luma's error body shape: {"detail": "..."}.
type wireErrorDetail struct {
	Detail string `json:"detail"`
}

// errorMessage tries to best-effort parse Luma's error body shape
// {"detail":"..."}. Falls back to the raw body if parsing fails or no
// message can be extracted.
func errorMessage(body []byte) string {
	var we wireErrorDetail
	if err := json.Unmarshal(body, &we); err == nil && we.Detail != "" {
		return we.Detail
	}
	return string(body)
}

// ---- provider options ----

// applyProviderOptions merges providerOptions["luma"] (when it is a
// non-empty map[string]any) top-level into the already-marshaled JSON
// request object reqBytes, entries from the option map winning over
// whatever the SDK built. Returns reqBytes unchanged (no unmarshal/marshal
// round trip) when there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["luma"].(map[string]any)
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
