// Package openaicompat implements the go-ai-sdk provider interfaces against
// the OpenAI Chat Completions and Embeddings wire format, parameterized so
// that OpenAI-compatible providers (OpenAI itself, and any provider that
// exposes an OpenAI-compatible API) can reuse it by supplying a Config.
package openaicompat

import "net/http"

// Config parameterizes an OpenAI-compatible provider.
type Config struct {
	Name       string // provider.LanguageModel.ProviderName() value, e.g. "groq"
	APIKey     string
	BaseURL    string       // no trailing slash, e.g. "https://api.groq.com/openai/v1"
	HTTPClient *http.Client // nil -> http.DefaultClient
	NativeJSON bool         // Capabilities().NativeJSON
	EmbedBatch int          // EmbeddingModel.MaxBatchSize(); callers only construct embedding models when > 0
}

// client returns the configured *http.Client, falling back to
// http.DefaultClient when none was supplied.
func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
