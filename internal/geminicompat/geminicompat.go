// Package geminicompat implements the go-ai-sdk provider interfaces against
// Google's Generative Language (Gemini) wire format, parameterized so that
// Gemini-compatible providers can reuse it by supplying a Config.
package geminicompat

import (
	"context"
	"net/http"
	"strings"
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

	// AuthHeaderName names the HTTP header Authorize sets its credential
	// on, so extra headers from provider.Call.Headers can avoid clobbering
	// it. Empty defaults to "Authorization" (Vertex's bearer token); Google
	// AI Studio's API key goes on "x-goog-api-key" instead and sets this
	// explicitly.
	AuthHeaderName string
}

// authHeaderName returns c.AuthHeaderName, defaulting to "Authorization".
func (c Config) authHeaderName() string {
	if c.AuthHeaderName != "" {
		return c.AuthHeaderName
	}
	return "Authorization"
}

// applyExtraHeaders sets each entry of headers on req, skipping any entry
// whose key case-insensitively matches authHeaderName — the caller must not
// be able to override the provider's own authentication header via
// provider.Call.Headers.
func applyExtraHeaders(req *http.Request, headers map[string]string, authHeaderName string) {
	for k, v := range headers {
		if strings.EqualFold(k, authHeaderName) {
			continue
		}
		req.Header.Set(k, v)
	}
}

// client returns the configured *http.Client, falling back to
// http.DefaultClient when none was supplied.
func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
