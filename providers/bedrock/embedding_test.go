package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// embedFixtureState records the last embedding request the fixture server
// saw, so tests can assert on wire-level request shape (body + SigV4
// headers), mirroring fixtureState in bedrock_test.go.
type embedFixtureState struct {
	mu       sync.Mutex
	lastBody titanEmbedRequest
	lastAuth string
	lastDate string
	lastPath string
}

func (fs *embedFixtureState) record(req titanEmbedRequest, r *http.Request) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.lastBody = req
	fs.lastAuth = r.Header.Get("Authorization")
	fs.lastDate = r.Header.Get("X-Amz-Date")
	fs.lastPath = r.URL.EscapedPath()
}

func (fs *embedFixtureState) snapshot() (titanEmbedRequest, string, string, string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.lastBody, fs.lastAuth, fs.lastDate, fs.lastPath
}

func newEmbeddingFixtureServer(t *testing.T) (*httptest.Server, *embedFixtureState) {
	t.Helper()
	fs := &embedFixtureState{}

	mux := http.NewServeMux()
	mux.HandleFunc("/model/{id}/invoke", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "AWS4-HMAC-SHA256") || !strings.Contains(got, "SignedHeaders=") {
			t.Errorf("fixture: Authorization header malformed: %q", got)
		}
		if got := r.Header.Get("X-Amz-Date"); got == "" {
			t.Errorf("fixture: X-Amz-Date header missing")
		}

		var req titanEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("fixture: decode request: %v", err)
		}
		fs.record(req, r)

		switch req.InputText {
		case "fail 400":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			w.Write([]byte(`{"message":"bad request"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(titanEmbedResponse{
			Embedding:           []float64{0.1, 0.2, 0.3},
			InputTextTokenCount: len(req.InputText),
		})
	})

	server := httptest.NewServer(mux)
	return server, fs
}

func TestEmbeddingModel_RequestShapeAndUsage(t *testing.T) {
	server, fs := newEmbeddingFixtureServer(t)
	t.Cleanup(server.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	model := p.EmbeddingModel("amazon.titan-embed-text-v2:0")

	if got := model.ModelID(); got != "amazon.titan-embed-text-v2:0" {
		t.Errorf("ModelID() = %q, want %q", got, "amazon.titan-embed-text-v2:0")
	}
	if got := model.ProviderName(); got != providerName {
		t.Errorf("ProviderName() = %q, want %q", got, providerName)
	}
	if got := model.MaxBatchSize(); got != 1 {
		t.Errorf("MaxBatchSize() = %d, want 1", got)
	}

	resp, err := model.Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) != 1 {
		t.Fatalf("Embeddings = %d, want 1", len(resp.Embeddings))
	}
	want := []float64{0.1, 0.2, 0.3}
	got := resp.Embeddings[0]
	if len(got) != len(want) {
		t.Fatalf("Embeddings[0] = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Embeddings[0][%d] = %v, want %v", i, got[i], want[i])
		}
	}

	wantTokens := len("hello world")
	if resp.Usage.InputTokens != wantTokens {
		t.Errorf("Usage.InputTokens = %d, want %d", resp.Usage.InputTokens, wantTokens)
	}
	if resp.Usage.TotalTokens != wantTokens {
		t.Errorf("Usage.TotalTokens = %d, want %d", resp.Usage.TotalTokens, wantTokens)
	}

	body, auth, date, path := fs.snapshot()
	if body.InputText != "hello world" {
		t.Errorf("request body inputText = %q, want %q", body.InputText, "hello world")
	}
	if auth == "" || date == "" {
		t.Fatalf("fixture did not record Authorization/X-Amz-Date")
	}
	const wantPath = "/model/amazon.titan-embed-text-v2%3A0/invoke"
	if path != wantPath {
		t.Errorf("request path = %q, want %q", path, wantPath)
	}
}

func TestEmbeddingModel_MultipleValuesErrors(t *testing.T) {
	server, _ := newEmbeddingFixtureServer(t)
	t.Cleanup(server.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKID", "secret", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	model := p.EmbeddingModel("amazon.titan-embed-text-v2:0")

	_, err := model.Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("Embed: want error for len(values) > 1 (Titan accepts one text per call), got nil")
	}
}

func TestEmbeddingModel_EmptyValuesErrors(t *testing.T) {
	server, _ := newEmbeddingFixtureServer(t)
	t.Cleanup(server.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKID", "secret", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	model := p.EmbeddingModel("amazon.titan-embed-text-v2:0")

	_, err := model.Embed(context.Background(), nil)
	if err == nil {
		t.Fatal("Embed: want error for zero values, got nil")
	}
}

func TestEmbeddingModel_Error(t *testing.T) {
	server, _ := newEmbeddingFixtureServer(t)
	t.Cleanup(server.Close)

	p := New(
		WithRegion("us-east-1"),
		WithCredentials("AKID", "secret", ""),
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)
	model := p.EmbeddingModel("amazon.titan-embed-text-v2:0")

	_, err := model.Embed(context.Background(), []string{"fail 400"})
	if err == nil {
		t.Fatal("Embed: want error, got nil")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *ai.APICallError", err, err)
	}
	if apiErr.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
	}
}
