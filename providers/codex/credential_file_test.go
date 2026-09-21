package codex_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/auth"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/codex"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestExpiredCredentialsRefreshAutomatically covers both automatic-refresh
// paths: an expired token is refreshed before the request, the backend sees
// the new token and account ID, and WithCredentialFile saves the rotation.
func TestExpiredCredentialsRefreshAutomatically(t *testing.T) {
	access := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fresh-account"}}`)) + ".sig"
	expired := codex.Credential{Access: "expired-access", Refresh: "old-refresh", Expires: time.Now().Add(-time.Minute), AccountID: "old-account"}
	path := filepath.Join(t.TempDir(), "codex.json")

	for name, opt := range map[string]codex.Option{
		"WithCredentials":    codex.WithCredentials(expired),
		"WithCredentialFile": codex.WithCredentialFile(path),
	} {
		t.Run(name, func(t *testing.T) {
			if err := auth.Save(path, auth.Credentials(expired)); err != nil {
				t.Fatal(err)
			}
			refreshes := 0
			var gotAuth, gotAccount string
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				body := `{"id":"fixture","output":[],"stop_reason":"stop"}`
				if r.URL.Host == "auth.openai.com" {
					refreshes++
					body = `{"access_token":"` + access + `","refresh_token":"rotated-refresh","expires_in":3600}`
				} else {
					gotAuth, gotAccount = r.Header.Get("Authorization"), r.Header.Get("ChatGPT-Account-ID")
				}
				header := http.Header{"Content-Type": []string{"application/json"}}
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}

			model := codex.New(codex.WithBaseURL("https://codex.test"), codex.WithHTTPClient(client), opt).Model("fixture-model")
			for range 2 {
				if _, err := model.Generate(context.Background(), provider.Call{Messages: []provider.Message{provider.UserText("hi")}}); err != nil {
					t.Fatalf("Generate: %v", err)
				}
			}
			if gotAuth != "Bearer "+access || gotAccount != "fresh-account" {
				t.Errorf("backend saw (%q, %q), want the refreshed token and account", gotAuth, gotAccount)
			}
			if refreshes != 1 {
				t.Errorf("refreshes = %d, want 1 (the refreshed token is reused)", refreshes)
			}
			if name == "WithCredentialFile" {
				saved, err := auth.Load(path)
				if err != nil || saved.Refresh != "rotated-refresh" {
					t.Errorf("file after refresh = %+v, %v; want the rotated refresh token", saved, err)
				}
			}
		})
	}
}
