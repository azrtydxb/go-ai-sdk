// Package mixedbread implements the go-ai-sdk provider.RerankingModel
// interface against Mixedbread AI's rerank API.
package mixedbread

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultBaseURL = "https://api.mixedbread.com/v1"
	providerName   = "mixedbread"
)

// Provider is a Mixedbread-backed provider.RerankingModel factory.
type Provider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the Authorization header. Defaults
// to os.Getenv("MXBAI_API_KEY").
func WithAPIKey(k string) Option {
	return func(p *Provider) { p.apiKey = k }
}

// WithBaseURL overrides the API base URL (default
// "https://api.mixedbread.com/v1").
func WithBaseURL(u string) Option {
	return func(p *Provider) { p.baseURL = u }
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option {
	return func(p *Provider) { p.httpClient = c }
}

// New creates a new Mixedbread Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:     os.Getenv("MXBAI_API_KEY"),
		baseURL:    defaultBaseURL,
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// RerankingModel returns a provider.RerankingModel for the given Mixedbread
// rerank model ID (e.g. "mixedbread-ai/mxbai-rerank-large-v1").
func (p *Provider) RerankingModel(id string) provider.RerankingModel {
	return &rerankingModel{provider: p, modelID: id}
}

func (p *Provider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// ---- wire types ----

type wireError struct {
	Detail string `json:"detail"`
}

// errorMessage tries to parse Mixedbread's {"detail":...} error body
// shape. Falls back to the raw body if parsing fails or no detail field is
// present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Detail != "" {
		return we.Detail
	}
	return string(body)
}

// applyProviderOptions merges providerOptions["mixedbread"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip) when
// there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["mixedbread"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("mixedbread: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), errorMessage(body))
}
