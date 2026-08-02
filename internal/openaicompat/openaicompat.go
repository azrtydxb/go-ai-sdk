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

	// MaxTokensParam is the wire field name used to send provider.Call's
	// MaxTokens. Empty defaults to "max_completion_tokens" (OpenAI's current
	// field name). Some OpenAI-compatible servers (Perplexity, Fireworks,
	// Together, DeepSeek) still document the older "max_tokens" name and
	// silently ignore "max_completion_tokens", so those presets set this to
	// "max_tokens".
	MaxTokensParam string

	// JSONObjectOnly restricts ResponseFormat{Type:"json"} to the wire's
	// {"type":"json_object"} shape, dropping any Schema even when one is
	// provided. Set this for providers whose response_format only accepts
	// json_object (not json_schema) — e.g. DeepSeek.
	JSONObjectOnly bool
}

// client returns the configured *http.Client, falling back to
// http.DefaultClient when none was supplied.
func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
