// Package revai implements the go-ai-sdk provider.TranscriptionModel
// interface against Rev.ai's asynchronous speech-to-text API: a job is
// created from the uploaded audio, the job is polled until it reaches a
// terminal state, then the transcript is fetched.
//
// This provider is implemented against the documented wire format but has
// not been verified against the live API.
package revai

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	providerName        = "revai"
	defaultBaseURL      = "https://api.rev.ai"
	defaultPollInterval = 500 * time.Millisecond
)

// Provider is a Rev.ai-backed provider.TranscriptionModel factory.
type Provider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	pollInterval time.Duration
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "Authorization: Bearer <key>"
// header. Defaults to os.Getenv("REVAI_API_KEY"), falling back to
// os.Getenv("REV_AI_API_KEY") when that's empty.
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default "https://api.rev.ai").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// WithPollInterval overrides the interval between job status polls (default
// 500ms). Primarily a test hook so fixtures can poll fast.
func WithPollInterval(d time.Duration) Option {
	return func(p *Provider) { p.pollInterval = d }
}

// New creates a new Rev.ai Provider.
func New(opts ...Option) *Provider {
	apiKey := os.Getenv("REVAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("REV_AI_API_KEY")
	}
	p := &Provider{
		apiKey:       apiKey,
		baseURL:      defaultBaseURL,
		httpClient:   http.DefaultClient,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// TranscriptionModel returns a provider.TranscriptionModel. Rev.ai's
// speech-to-text API is not versioned by model id, so id is accepted for
// interface conformance but otherwise unused.
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
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}

// wireError matches Rev.ai's error body shape:
// {"title":"...","detail":"..."} (RFC 7807 Problem Details).
type wireError struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// errorMessage tries to parse Rev.ai's error body shape
// {"title":"...","detail":"..."}, preferring detail over title. Falls back
// to the raw body if parsing fails or neither field is present.
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

// extForMediaType returns a plausible file extension (including the leading
// dot) for a MIME media type, used to build a filename for the multipart
// upload. Returns "" when the type is unknown or empty.
func extForMediaType(mediaType string) string {
	switch mediaType {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "video/mp4":
		return ".mp4"
	}
	if mediaType == "" {
		return ""
	}
	for i := len(mediaType) - 1; i >= 0; i-- {
		if mediaType[i] == '/' {
			if i+1 < len(mediaType) {
				return "." + mediaType[i+1:]
			}
			return ""
		}
	}
	return ""
}
