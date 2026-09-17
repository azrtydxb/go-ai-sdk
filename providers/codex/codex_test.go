package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func fakeJWT(claims map[string]any) string {
	header := `{"alg":"none","typ":"JWT"}`
	h := base64.RawURLEncoding.EncodeToString([]byte(header))
	c, _ := json.Marshal(claims)
	p := base64.RawURLEncoding.EncodeToString(c)
	return h + "." + p + "."
}

func testCodexCred(accountID string) codexauth.Credential {
	claims := map[string]any{
		"sub": "user123",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	return codexauth.Credential{
		Access:    fakeJWT(claims),
		Refresh:   "refresh-test",
		Expires:   future(),
		AccountID: accountID,
	}
}

func future() time.Time { return time.Now().Add(24 * time.Hour) }

func sseFixtureServer(chunks ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	}))
}

func TestNew_Model(t *testing.T) {
	cred := testCodexCred("acc-1")
	p := New(WithCredentials(cred))
	m := p.Model("gpt-test")

	if m.ModelID() != "gpt-test" {
		t.Errorf("ModelID() = %q, want gpt-test", m.ModelID())
	}
	if m.ProviderName() != "openai-codex" {
		t.Errorf("ProviderName() = %q, want openai-codex", m.ProviderName())
	}
}

func TestRegistryResolvesCodexProvider(t *testing.T) {
	reg := ai.NewRegistry()
	reg.Register("codex", New(WithCredentials(testCodexCred("acc-1"))))

	m, err := reg.LanguageModel("codex:gpt-test")
	if err != nil {
		t.Fatalf("LanguageModel: %v", err)
	}
	if m.ProviderName() != "openai-codex" || m.ModelID() != "gpt-test" {
		t.Fatalf("resolved model = %s/%s, want openai-codex/gpt-test", m.ProviderName(), m.ModelID())
	}
}

func TestCodex_StreamText(t *testing.T) {
	cred := testCodexCred("acc-1")
	srv := sseFixtureServer(
		`{"type":"response.output_item.added","item":{"id":"fc_1","type":"message"}}`,
		`{"type":"response.delta","delta":{"type":"delta","text":"Hello"}}`,
		`{"type":"response.output_item.done","item":{"id":"fc_1","type":"message"}}`,
		`{"type":"response.completed","stop_reason":"stop"}`,
	)
	defer srv.Close()

	p := New(WithCredentials(cred), WithBaseURL(srv.URL))
	m := p.Model("gpt-test")

	sr, err := m.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hello")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var texts []provider.TextDelta
	for part := range sr.Parts() {
		if td, ok := part.(provider.TextDelta); ok {
			texts = append(texts, td)
		}
	}
	if len(texts) != 1 {
		t.Fatalf("got %d TextDelta, want 1", len(texts))
	}
	if texts[0].Text != "Hello" {
		t.Errorf("Text = %q, want Hello", texts[0].Text)
	}
}

func TestCodex_MissingCredentials(t *testing.T) {
	p := New()
	m := p.Model("gpt-test")
	_, err := m.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hello")},
	})
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
}
