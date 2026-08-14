package geminicompat

// Wire-level tests: provider.Call-family Headers must reach the outgoing
// request for the embedding and image paths (which build their own
// http.Request rather than going through languageModel.doRequest), and a
// header whose name case-insensitively matches the provider's auth header
// (cfg.authHeaderName()) must NOT be able to override the credential
// Authorize set — same contract as the language path
// (language_model.go:doRequest).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestEmbedding_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"embeddings":[{"values":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer real-token")
			return nil
		},
		EmbedBatch: 100,
	}
	model := NewEmbeddingModel(cfg, "text-embedding-test")
	em, ok := model.(provider.EmbeddingModelWithOptions)
	if !ok {
		t.Fatal("embeddingModel does not implement EmbeddingModelWithOptions")
	}

	_, err := em.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values:  []string{"a"},
		Headers: map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-token" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-token")
	}
}

func TestImage_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[]}`))
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer real-token")
			return nil
		},
	}
	model := NewImageModel(cfg, "imagen-test")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:  "a cat",
		Headers: map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-token" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-token")
	}
}

// TestEmbedding_HeadersRespectCustomAuthHeaderName verifies the skip rule
// uses cfg.AuthHeaderName (e.g. Google AI Studio's "x-goog-api-key") rather
// than a hardcoded "Authorization".
func TestEmbedding_HeadersRespectCustomAuthHeaderName(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Goog-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"embeddings":[{"values":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("x-goog-api-key", "real-key")
			return nil
		},
		AuthHeaderName: "x-goog-api-key",
		EmbedBatch:     100,
	}
	model := NewEmbeddingModel(cfg, "text-embedding-test")
	em := model.(provider.EmbeddingModelWithOptions)

	_, err := em.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values:  []string{"a"},
		Headers: map[string]string{"x-goog-api-key": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}
	if gotAuth != "real-key" {
		t.Errorf("x-goog-api-key header = %q, want %q (must not be overridden by Headers)", gotAuth, "real-key")
	}
}
