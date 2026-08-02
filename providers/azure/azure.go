// Package azure provides the Azure OpenAI provider. Azure OpenAI exposes an
// OpenAI-compatible "v1" surface, so this package is a preset over the
// shared openaicompat base — with one twist: authentication goes over the
// "api-key" header instead of "Authorization: Bearer", and model IDs are
// Azure DEPLOYMENT names, not OpenAI model names.
package azure

import (
	"fmt"
	"net/http"
	"os"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// Provider is an Azure-OpenAI-backed provider.LanguageModel / EmbeddingModel
// factory.
type Provider struct {
	apiKey       string
	resourceName string
	baseURL      string
	httpClient   *http.Client
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the API key sent via the "api-key" header. Defaults to
// os.Getenv("AZURE_API_KEY").
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithResourceName sets the Azure resource name, used to derive the base
// URL "https://{resource}.openai.azure.com/openai/v1". Defaults to
// os.Getenv("AZURE_RESOURCE_NAME"). Ignored when WithBaseURL is also given.
func WithResourceName(name string) Option { return func(p *Provider) { p.resourceName = name } }

// WithBaseURL overrides the API base URL entirely, taking precedence over
// WithResourceName / AZURE_RESOURCE_NAME.
func WithBaseURL(u string) Option { return func(p *Provider) { p.baseURL = u } }

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// New creates a new Azure OpenAI Provider.
func New(opts ...Option) *Provider {
	p := &Provider{
		apiKey:       os.Getenv("AZURE_API_KEY"),
		resourceName: os.Getenv("AZURE_RESOURCE_NAME"),
		httpClient:   http.DefaultClient,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// resolvedBaseURL returns p.baseURL if set, otherwise the base URL derived
// from p.resourceName, otherwise "" (neither configured).
func (p *Provider) resolvedBaseURL() string {
	if p.baseURL != "" {
		return p.baseURL
	}
	if p.resourceName != "" {
		return fmt.Sprintf("https://%s.openai.azure.com/openai/v1", p.resourceName)
	}
	return ""
}

// Model returns a provider.LanguageModel for the given Azure deployment
// name. If neither a resource name nor a base URL was configured, requests
// against the returned model fail with an error from Generate/Stream.
func (p *Provider) Model(id string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(openaicompat.Config{
		Name:         "azure",
		APIKey:       p.apiKey,
		BaseURL:      p.resolvedBaseURL(),
		HTTPClient:   p.httpClient,
		NativeJSON:   true,
		APIKeyHeader: "api-key",
	}, id)
}

// EmbeddingModel returns a provider.EmbeddingModel for the given Azure
// deployment name. If neither a resource name nor a base URL was
// configured, requests against the returned model fail with an error from
// Embed.
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(openaicompat.Config{
		Name:         "azure",
		APIKey:       p.apiKey,
		BaseURL:      p.resolvedBaseURL(),
		HTTPClient:   p.httpClient,
		EmbedBatch:   2048,
		APIKeyHeader: "api-key",
	}, id)
}
