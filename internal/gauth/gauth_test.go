package gauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
)

// ---- test helpers ----

// generateTestKey returns a fresh RSA key pair and the PEM-encoded PKCS8
// private key, suitable for embedding in a service-account JSON fixture.
func generateTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return priv, string(pemBytes)
}

func serviceAccountJSON(t *testing.T, email, privateKeyPEM, tokenURI string) []byte {
	t.Helper()
	sa := map[string]string{
		"type":         "service_account",
		"client_email": email,
		"private_key":  privateKeyPEM,
	}
	if tokenURI != "" {
		sa["token_uri"] = tokenURI
	}
	b, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal service account JSON: %v", err)
	}
	return b
}

// decodeJWT splits a JWT into its three base64url segments and returns the
// decoded header, decoded claims, and raw signature bytes.
func decodeJWT(t *testing.T, jwt string) (header, claims map[string]any, signature []byte, signingInput string) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments, want 3: %q", len(parts), jwt)
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	header = map[string]any{}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	claims = map[string]any{}
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return header, claims, sig, parts[0] + "." + parts[1]
}

// newTokenServer returns an httptest.Server that decodes the incoming JWT
// bearer assertion, verifies its RS256 signature against pub, and responds
// with a fixed access token / expires_in. It records the number of requests
// received in reqCount and returns the last-seen claims via lastClaims.
func newTokenServer(t *testing.T, pub *rsa.PublicKey, accessToken string, expiresIn int, reqCount *int32, lastClaims *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(reqCount, 1)

		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		if got := r.Form.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q, want jwt-bearer", got)
		}
		assertion := r.Form.Get("assertion")
		if assertion == "" {
			t.Errorf("missing assertion form value")
			http.Error(w, "missing assertion", 400)
			return
		}

		header, claims, sig, signingInput := decodeJWT(t, assertion)
		if header["alg"] != "RS256" {
			t.Errorf("header alg = %v, want RS256", header["alg"])
		}
		if header["typ"] != "JWT" {
			t.Errorf("header typ = %v, want JWT", header["typ"])
		}

		hashed := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], sig); err != nil {
			t.Errorf("JWT signature does not verify: %v", err)
		}

		if lastClaims != nil {
			*lastClaims = claims
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d,"token_type":"Bearer"}`, accessToken, expiresIn)
	}))
}

// ---- tests ----

func TestNewServiceAccountTokenSource_InvalidJSON(t *testing.T) {
	if _, err := NewServiceAccountTokenSource([]byte("not json")); err == nil {
		t.Fatal("want error for invalid JSON, got nil")
	}
}

func TestNewServiceAccountTokenSource_MissingFields(t *testing.T) {
	_, priv := generateTestKey(t)
	if _, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "", priv, "")); err == nil {
		t.Fatal("want error for missing client_email, got nil")
	}
	if _, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", "", "")); err == nil {
		t.Fatal("want error for missing private_key, got nil")
	}
}

func TestNewServiceAccountTokenSource_InvalidPrivateKeyPEM(t *testing.T) {
	_, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", "not a pem key", ""))
	if err == nil {
		t.Fatal("want error for unparseable private key, got nil")
	}
}

func TestNewServiceAccountTokenSourceFromFile(t *testing.T) {
	priv, pemStr := generateTestKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(path, serviceAccountJSON(t, "sa@example.com", pemStr, ""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ts, err := NewServiceAccountTokenSourceFromFile(path)
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSourceFromFile: %v", err)
	}

	var reqCount int32
	srv := newTokenServer(t, &priv.PublicKey, "tok-from-file", 3600, &reqCount, nil)
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "tok-from-file" {
		t.Errorf("Token() = %q, want %q", tok, "tok-from-file")
	}
}

func TestNewServiceAccountTokenSourceFromFile_MissingFile(t *testing.T) {
	if _, err := NewServiceAccountTokenSourceFromFile("/nonexistent/path/sa.json"); err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}

func TestServiceAccountTokenSource_TokenFlow(t *testing.T) {
	priv, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	var reqCount int32
	var claims map[string]any
	srv := newTokenServer(t, &priv.PublicKey, "access-token-1", 3600, &reqCount, &claims)
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "access-token-1" {
		t.Errorf("Token() = %q, want %q", tok, "access-token-1")
	}
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}

	if claims["iss"] != "sa@example.com" {
		t.Errorf("claims[iss] = %v, want sa@example.com", claims["iss"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("claims[scope] = %v, want cloud-platform scope", claims["scope"])
	}
	if claims["aud"] != srv.URL {
		t.Errorf("claims[aud] = %v, want %v", claims["aud"], srv.URL)
	}
	iat, iok := claims["iat"].(float64)
	exp, eok := claims["exp"].(float64)
	if !iok || !eok {
		t.Fatalf("claims[iat]/[exp] not numeric: %+v", claims)
	}
	if exp-iat != 3600 {
		t.Errorf("exp - iat = %v, want 3600 (1h)", exp-iat)
	}

	// Second call within the cache window must not hit the network again.
	tok2, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token (cached): %v", err)
	}
	if tok2 != "access-token-1" {
		t.Errorf("cached Token() = %q, want %q", tok2, "access-token-1")
	}
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("request count after cached call = %d, want 1 (no HTTP)", got)
	}
}

func TestServiceAccountTokenSource_RefreshesAfterExpiry(t *testing.T) {
	priv, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	var reqCount int32
	// expires_in of 60s means the cache TTL (expiry - 60s) is ~0, so the
	// very next call should refresh rather than serve from cache.
	srv := newTokenServer(t, &priv.PublicKey, "short-lived-token", 60, &reqCount, nil)
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("Token #1: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("Token #2: %v", err)
	}
	if got := atomic.LoadInt32(&reqCount); got != 2 {
		t.Fatalf("request count = %d, want 2 (token near-immediately expired, must refresh)", got)
	}
}

func TestServiceAccountTokenSource_TokenEndpointError(t *testing.T) {
	_, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	_, err = ts.Token(context.Background())
	if err == nil {
		t.Fatal("want error for non-2xx token endpoint response, got nil")
	}
	var tee *TokenEndpointError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want *TokenEndpointError", err)
	}
	if tee.StatusCode != 401 {
		t.Errorf("StatusCode = %d, want 401", tee.StatusCode)
	}
	if tee.IsRetryable() {
		t.Error("401 should not be classified retryable")
	}
}

// TestServiceAccountTokenSource_TokenEndpoint5xxIsRetryable verifies that a
// transient (5xx) failure from the token endpoint is classified retryable
// by internal/retry.Do's own mechanism: retry.Do detects retryability via
// errors.As against the retry.Retryable interface (IsRetryable() bool), not
// via any concrete *ai.APICallError type, so a gauth-local error type
// implementing that method is sufficient -- no import of ai is required.
func TestServiceAccountTokenSource_TokenEndpoint5xxIsRetryable(t *testing.T) {
	_, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	_, err = ts.Token(context.Background())
	if err == nil {
		t.Fatal("want error for 503 token endpoint response, got nil")
	}

	var tee *TokenEndpointError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want *TokenEndpointError", err)
	}
	if !tee.IsRetryable() {
		t.Error("503 should be classified retryable")
	}

	// End-to-end: retry.Do must actually retry it.
	calls := 0
	_, retryErr := retry.Do(context.Background(), 2, func() (string, error) {
		calls++
		return ts.Token(context.Background())
	})
	if calls != 3 {
		t.Fatalf("calls = %d, want 3 (retry.Do should retry a retryable TokenEndpointError)", calls)
	}
	var ex *retry.ExhaustedError
	if !errors.As(retryErr, &ex) {
		t.Fatalf("retryErr = %v, want *retry.ExhaustedError", retryErr)
	}
}

func TestServiceAccountTokenSource_ContextCancellation(t *testing.T) {
	_, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}
	ts.SetTokenURL("http://127.0.0.1:0/token")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ts.Token(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
	}
}

func TestServiceAccountTokenSource_DefaultTokenURL(t *testing.T) {
	_, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}
	if got := ts.tokenURL; got != "https://oauth2.googleapis.com/token" {
		t.Errorf("default tokenURL = %q, want %q", got, "https://oauth2.googleapis.com/token")
	}
}

// TestServiceAccountTokenSource_SingleFlight verifies that N concurrent
// Token() calls on a cold cache mint exactly one token: the mutex is held
// across the whole refresh, so only the first caller to acquire it performs
// the JWT-bearer round trip, and every other caller blocks on the lock and
// then observes the freshly cached, still-valid token instead of making its
// own request.
func TestServiceAccountTokenSource_SingleFlight(t *testing.T) {
	priv, pemStr := generateTestKey(t)
	ts, err := NewServiceAccountTokenSource(serviceAccountJSON(t, "sa@example.com", pemStr, ""))
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	var reqCount int32
	srv := newTokenServer(t, &priv.PublicKey, "single-flight-token", 3600, &reqCount, nil)
	defer srv.Close()
	ts.SetTokenURL(srv.URL)

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	toks := make([]string, n)
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			tok, err := ts.Token(context.Background())
			toks[i] = tok
			errs[i] = err
		}(i)
	}
	start.Done()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: Token: %v", i, err)
		}
		if toks[i] != "single-flight-token" {
			t.Errorf("goroutine %d: Token() = %q, want %q", i, toks[i], "single-flight-token")
		}
	}
	if got := atomic.LoadInt32(&reqCount); got != 1 {
		t.Fatalf("token endpoint request count = %d, want exactly 1 (single-flight)", got)
	}
}

func TestStaticTokenSource(t *testing.T) {
	ts := StaticTokenSource("static-token")
	tok, err := ts.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "static-token" {
		t.Errorf("Token() = %q, want %q", tok, "static-token")
	}
}

// ensure TokenSource interface is satisfied
var (
	_ TokenSource = StaticTokenSource("")
	_ TokenSource = (*ServiceAccountTokenSource)(nil)
)
