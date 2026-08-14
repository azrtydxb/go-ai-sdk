// Package assemblyai implements the go-ai-sdk provider.TranscriptionModel
// interface against AssemblyAI's asynchronous speech-to-text API: audio is
// uploaded, a transcript is created from the resulting URL, then the
// transcript is polled until it reaches a terminal state.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package assemblyai

import (
	"net/http"
	"os"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName        = "assemblyai"
	defaultBaseURL      = "https://api.assemblyai.com"
	defaultPollInterval = 500 * time.Millisecond
)

// Provider is an AssemblyAI-backed provider.TranscriptionModel factory.
type Provider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "authorization: <key>" header
// (no "Bearer" prefix). Defaults to os.Getenv("ASSEMBLYAI_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.assemblyai.com").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// WithPollInterval overrides the interval between transcript status polls
// (default 500ms). Primarily a test hook so fixtures can poll fast.
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) { p.pollInterval = d }
}

// New creates a new AssemblyAI Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:       os.Getenv("ASSEMBLYAI_API_KEY"),
		baseURL:      defaultBaseURL,
		httpClient:   http.DefaultClient,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// TranscriptionModel returns a provider.TranscriptionModel for the given
// AssemblyAI speech model ID (e.g. "universal"). An empty id omits the
// "speech_model" field from the transcript creation request, letting
// AssemblyAI use its own default.
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel {
	return &transcriptionModel{provider: p, modelID: id}
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
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), providerutil.ErrorMessage(body))
}
