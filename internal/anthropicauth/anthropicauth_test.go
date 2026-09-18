package anthropicauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- test helpers ----

// newTokenServer returns an httptest.Server that responds with a token
// response containing the given accessToken and expiresIn. It records the
// JSON request body so tests can verify the request shape.
func newTokenServer(t *testing.T, accessToken string, expiresIn int, reqCount *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(reqCount, 1)

		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q, want application/json", accept)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode JSON request body: %v", err)
		}
		if payload["client_id"] != clientID {
			t.Errorf("client_id = %q, want %q", payload["client_id"], clientID)
		}
		if payload["grant_type"] == "authorization_code" && payload["state"] == "" {
			t.Error("authorization_code request missing state")
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"access_token":  accessToken,
			"refresh_token": "refresh-" + accessToken,
			"expires_in":    expiresIn,
			"token_type":    "Bearer",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func newErrorTokenServer(t *testing.T, statusCode int, bodyStr string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(bodyStr))
	}))
}

func newCallbackServer(t *testing.T, code, state string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>OK</body></html>"))
		_ = q
		_ = code
		_ = state
	}))
}

// ---- tests ----

func TestGeneratePKCE(t *testing.T) {
	v1, c1 := generatePKCE()
	v2, c2 := generatePKCE()

	if v1 == "" {
		t.Error("verifier should not be empty")
	}
	if c1 == "" {
		t.Error("challenge should not be empty")
	}
	if v1 == v2 {
		t.Error("two calls should produce different verifiers")
	}
	if c1 == c2 {
		t.Error("two calls should produce different challenges")
	}

	// Verify S256: challenge = Base64URL(SHA256(verifier))
	hash := sha256.Sum256([]byte(v1))
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	if c1 != expected {
		t.Errorf("challenge = %q, want %q", c1, expected)
	}

	// Verify verifier is valid base64url (no padding).
	if strings.Contains(v1, "=") {
		t.Error("verifier should not contain padding")
	}
	if strings.Contains(c1, "=") {
		t.Error("challenge should not contain padding")
	}
}

func TestStaticTokenSource(t *testing.T) {
	ts := StaticTokenSource("static-access-token")
	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "static-access-token" {
		t.Errorf("Token() = %q, want %q", tok, "static-access-token")
	}
}

func TestSaveLoadCredentialsRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "anthropic.json")
	creds := Credentials{Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour)}

	if err := SaveCredentials(path, creds); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", got)
	}

	loaded, err := LoadCredentials(path)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if loaded.Access != creds.Access || loaded.Refresh != creds.Refresh {
		t.Fatalf("loaded credentials = %+v, want %+v", loaded, creds)
	}

	ts := NewOAuthTokenSourceWithCredentials(loaded)
	if got := ts.Credentials(); got.Access != creds.Access || got.Refresh != creds.Refresh {
		t.Fatalf("TokenSource credentials = %+v, want %+v", got, creds)
	}
}

func TestOAuthTokenSource_ExchangeCode(t *testing.T) {
	src := NewOAuthTokenSource()

	var reqCount int32
	srv := newTokenServer(t, "test-access-token", 3600, &reqCount)
	defer srv.Close()

	// Redirect the token endpoint to our fake server.
	src.SetHTTPClient(&http.Client{Transport: &roundTripper{baseURL: srv.URL}})

	ctx := context.Background()
	creds, err := src.exchangeCode(ctx, "auth-code-123", "verifier-state", "test-verifier", "test-challenge")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}

	if creds.Access != "test-access-token" {
		t.Errorf("creds.Access = %q, want %q", creds.Access, "test-access-token")
	}
	if creds.Refresh != "refresh-test-access-token" {
		t.Errorf("creds.Refresh = %q, want %q", creds.Refresh, "refresh-test-access-token")
	}
	if creds.Expires.IsZero() {
		t.Error("creds.Expires should not be zero")
	}
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestOAuthTokenSource_CacheAndRefresh(t *testing.T) {
	src := NewOAuthTokenSource()

	var reqCount int32
	srv := newTokenServer(t, "fresh-token", 3600, &reqCount)
	defer srv.Close()

	src.SetHTTPClient(&http.Client{Transport: &roundTripper{baseURL: srv.URL}})

	ctx := context.Background()

	// First, populate the cache via exchange.
	_, err := src.exchangeCode(ctx, "code", "state", "verifier", "challenge")
	if err != nil {
		t.Fatalf("exchangeCode: %v", err)
	}

	// Token() should return the cached token without a new request.
	tok, err := src.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "fresh-token" {
		t.Errorf("Token() = %q, want %q", tok, "fresh-token")
	}
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("request count = %d, want 1 (cached)", got)
	}

	// Simulate expiry by setting an old expiry.
	src.mu.Lock()
	src.expiry = time.Now().Add(-time.Hour)
	src.mu.Unlock()

	// Now Token() should trigger a refresh.
	tok2, err := src.Token(ctx)
	if err != nil {
		t.Fatalf("Token (after expiry): %v", err)
	}
	if tok2 != "fresh-token" {
		t.Errorf("Token() after refresh = %q, want %q", tok2, "fresh-token")
	}
	if got := atomic.LoadInt32(&reqCount); got != 2 {
		t.Fatalf("request count = %d, want 2 (one cache, one refresh)", got)
	}
}

func TestOAuthTokenSource_NoRefreshToken(t *testing.T) {
	src := NewOAuthTokenSource()
	_, err := src.Token(context.Background())
	if err == nil {
		t.Fatal("want error when no refresh token, got nil")
	}
	if !errors.Is(err, errNoRefreshToken) {
		t.Errorf("err = %v, want errNoRefreshToken", err)
	}
}

func TestOAuthTokenSource_TokenEndpointError(t *testing.T) {
	src := NewOAuthTokenSource()
	srv := newErrorTokenServer(t, 401, `{"error":"invalid_grant"}`)
	defer srv.Close()

	src.SetHTTPClient(&http.Client{Transport: &roundTripper{baseURL: srv.URL}})

	_, err := src.exchangeCode(context.Background(), "code", "state", "verifier", "challenge")
	if err == nil {
		t.Fatal("want error for 401, got nil")
	}
	var tee *TokenEndpointError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want *TokenEndpointError", err)
	}
	if tee.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", tee.StatusCode)
	}
	if tee.IsRetryable() {
		t.Error("401 should not be retryable")
	}
}

func TestOAuthTokenSource_TokenEndpoint5xxIsRetryable(t *testing.T) {
	src := NewOAuthTokenSource()
	srv := newErrorTokenServer(t, 503, `{"error":"unavailable"}`)
	defer srv.Close()

	src.SetHTTPClient(&http.Client{Transport: &roundTripper{baseURL: srv.URL}})

	_, err := src.exchangeCode(context.Background(), "code", "state", "verifier", "challenge")
	if err == nil {
		t.Fatal("want error for 503, got nil")
	}
	var tee *TokenEndpointError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want *TokenEndpointError", err)
	}
	if !tee.IsRetryable() {
		t.Error("503 should be retryable")
	}
}

func TestTokenEndpointError_Error(t *testing.T) {
	tee := &TokenEndpointError{StatusCode: 400, URL: "https://test.com", Body: "bad request"}
	s := tee.Error()
	if !strings.Contains(s, "400") {
		t.Errorf("error string should contain status code, got %q", s)
	}
	if !strings.Contains(s, "test.com") {
		t.Errorf("error string should contain URL, got %q", s)
	}
}

func TestParseAuthInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
	}{
		{
			name:      "plain code",
			input:     "auth-code-123",
			wantCode:  "auth-code-123",
			wantState: "",
		},
		{
			name:      "redirect URL",
			input:     "http://localhost:53692/callback?code=abc&state=xyz",
			wantCode:  "abc",
			wantState: "xyz",
		},
		{
			name:      "fragment format",
			input:     "auth-code#state-val",
			wantCode:  "auth-code",
			wantState: "state-val",
		},
		{
			name:      "query string",
			input:     "code=mycode&state=mystate",
			wantCode:  "mycode",
			wantState: "mystate",
		},
		{
			name:      "empty input",
			input:     "",
			wantCode:  "",
			wantState: "",
		},
		{
			name:      "whitespace input",
			input:     "  ",
			wantCode:  "",
			wantState: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, state := parseAuthInput(tt.input, "expected")
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if state != tt.wantState {
				t.Errorf("state = %q, want %q", state, tt.wantState)
			}
		})
	}
}

func TestCallbackServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	expectedState := "test-state-123"
	expectedChallenge := "test-challenge"

	srv, authURL, closeFn, err := startCallbackServer(expectedState, expectedChallenge)
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer closeFn()

	// Verify the authorize URL contains the expected params.
	if !strings.Contains(authURL, "response_type=code") {
		t.Error("auth URL should contain response_type=code")
	}
	if !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Error("auth URL should contain code_challenge_method=S256")
	}
	if !strings.Contains(authURL, "state="+expectedState) {
		t.Error("auth URL should contain state=" + expectedState)
	}

	// Simulate a callback request.
	host := srv.server.Addr
	if host == "" {
		host = "127.0.0.1:53692"
	}
	callbackURL := fmt.Sprintf("http://%s%s?code=test-code&state=%s",
		host, callbackPath, expectedState)

	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("callback status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Wait for the result via waitForCode.
	result, err := srv.waitForCode(ctx)
	if err != nil {
		t.Fatalf("waitForCode: %v", err)
	}
	if result.Code != "test-code" {
		t.Errorf("result.Code = %q, want %q", result.Code, "test-code")
	}
	if result.State != expectedState {
		t.Errorf("result.State = %q, want %q", result.State, expectedState)
	}
}

func TestCallbackServer_StateMismatch(t *testing.T) {
	srv, _, closeFn, err := startCallbackServer("correct-state", "challenge")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer closeFn()

	callbackURL := fmt.Sprintf("http://%s%s?code=test-code&state=wrong-state",
		srv.server.Addr, callbackPath)

	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback status = %d, want 400", resp.StatusCode)
	}
}

func TestCallbackServer_ContextCancellation(t *testing.T) {
	_, _, closeFn, err := startCallbackServer("state", "challenge")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer closeFn()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = (&callbackServer{resultCh: make(chan callbackResult, 1)}).waitForCode(ctx)
	if err == nil {
		t.Fatal("waitForCode with cancelled context should error")
	}
}

func TestRedirectURI(t *testing.T) {
	uri := redirectURI()
	expected := fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)
	if uri != expected {
		t.Errorf("redirectURI() = %q, want %q", uri, expected)
	}
}

// roundTripper is a minimal http.RoundTripper that forwards requests to baseURL.
type roundTripper struct {
	baseURL string
}

func (r *roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = r.baseURL[strings.Index(r.baseURL, "://")+3:]
	req.URL.Path = "/token"
	return http.DefaultTransport.RoundTrip(req)
}

// ensure TokenSource interface is satisfied
var (
	_ TokenSource = StaticTokenSource("")
	_ TokenSource = (*OAuthTokenSource)(nil)
)
