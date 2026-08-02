// Package geminicompat implements the go-ai-sdk provider interfaces against
// Google's Generative Language (Gemini) wire format, parameterized so that
// Gemini-compatible providers can reuse it by supplying a Config.
package geminicompat

import (
	"context"
	"net/http"
)

// Config parameterizes a Gemini-compatible provider.
type Config struct {
	Name       string       // provider.LanguageModel.ProviderName() value, e.g. "google"
	HTTPClient *http.Client // nil -> http.DefaultClient

	// EndpointFor returns the full URL for a model call, given the model ID
	// and method. method is one of "generateContent", "streamGenerateContent"
	// (the caller appends "?alt=sse" AFTER calling EndpointFor, so
	// implementations should return a query-free URL), or
	// "batchEmbedContents".
	EndpointFor func(modelID, method string) string

	// Authorize mutates req with authentication (typically a header) before
	// it is sent. Called once per request.
	Authorize func(ctx context.Context, req *http.Request) error

	// EmbedBatch is EmbeddingModel.MaxBatchSize(); callers only construct
	// embedding models when > 0.
	EmbedBatch int
}

// client returns the configured *http.Client, falling back to
// http.DefaultClient when none was supplied.
func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
