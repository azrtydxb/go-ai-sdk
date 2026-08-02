package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixturePredictInstance struct {
	Content string `json:"content"`
}

type fixturePredictRequest struct {
	Instances []fixturePredictInstance `json:"instances"`
}

func newEmbeddingFixtureServer(t *testing.T, wantBearer string) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer "+wantBearer)
		}
		wantPath := fmt.Sprintf("/projects/%s/locations/%s/publishers/google/models/%s:predict", testProject, testLocation, "text-embedding-test")
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		var req fixturePredictRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

		var sb strings.Builder
		sb.WriteString(`{"predictions":[`)
		for i, inst := range req.Instances {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, `{"embeddings":{"values":[%d.0,%d.0,%d.0],"statistics":{"truncated":false,"token_count":%d}}}`,
				i, i+1, i+2, len(inst.Content))
		}
		sb.WriteString(`]}`)

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sb.String()))
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestEmbeddingModel(t *testing.T) {
	const token = "embed-test-token"
	srv := newEmbeddingFixtureServer(t, token)
	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL(srv.URL),
		WithAccessToken(token),
	)
	model := p.EmbeddingModel("text-embedding-test")

	if got := model.ModelID(); got != "text-embedding-test" {
		t.Errorf("ModelID() = %q, want %q", got, "text-embedding-test")
	}
	if got := model.ProviderName(); got != "vertex" {
		t.Errorf("ProviderName() = %q, want %q", got, "vertex")
	}
	if got := model.MaxBatchSize(); got != 250 {
		t.Errorf("MaxBatchSize() = %d, want 250", got)
	}

	values := []string{"a", "bb", "ccc"}
	resp, err := model.Embed(context.Background(), values)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(resp.Embeddings) != 3 {
		t.Fatalf("Embeddings = %d, want 3", len(resp.Embeddings))
	}
	for i, v := range values {
		want := []float64{float64(i), float64(i + 1), float64(i + 2)}
		got := resp.Embeddings[i]
		if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Errorf("Embeddings[%d] = %v, want %v", i, got, want)
		}
		_ = v
	}

	wantTokens := len("a") + len("bb") + len("ccc")
	if resp.Usage.InputTokens != wantTokens {
		t.Errorf("Usage.InputTokens = %d, want %d", resp.Usage.InputTokens, wantTokens)
	}
	if resp.Usage.TotalTokens != wantTokens {
		t.Errorf("Usage.TotalTokens = %d, want %d", resp.Usage.TotalTokens, wantTokens)
	}
}

func TestEmbeddingModel_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"internal error"}}`))
	}))
	t.Cleanup(srv.Close)

	p := New(WithProject(testProject), WithLocation(testLocation), WithBaseURL(srv.URL), WithAccessToken("tok"))
	model := p.EmbeddingModel("text-embedding-test")

	_, err := model.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed: want error, got nil")
	}
}

func TestEmbeddingModel_NoCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "")
	p := New(WithProject(testProject), WithLocation(testLocation), WithBaseURL("http://example.invalid"))
	model := p.EmbeddingModel("text-embedding-test")

	_, err := model.Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed: want error, got nil")
	}
	if !strings.Contains(err.Error(), "vertex: no credentials configured") {
		t.Errorf("error = %v, want it to mention %q", err, "vertex: no credentials configured")
	}
}
