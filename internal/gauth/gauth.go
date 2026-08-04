// Package gauth mints OAuth2 access tokens for Google Cloud APIs from a
// service-account key, using only the standard library (RS256 JWT bearer
// grant, per https://developers.google.com/identity/protocols/oauth2/service-account).
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
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// defaultTokenURL is the Google OAuth2 token endpoint used when a
// service-account key does not specify its own token_uri.
const defaultTokenURL = "https://oauth2.googleapis.com/token"

// cloudPlatformScope is the OAuth2 scope requested for Vertex AI / Google
// Cloud API access.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// expiryLeeway is subtracted from a minted token's reported lifetime so
// cached tokens are refreshed slightly before they actually expire.
const expiryLeeway = 60 * time.Second

// TokenSource yields a bearer token for Google Cloud APIs.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenSource is a TokenSource that always returns itself as the
// token; Token never errors.
type StaticTokenSource string

// Token returns string(s). It never errors.
func (s StaticTokenSource) Token(ctx context.Context) (string, error) {
	return string(s), nil
}

// serviceAccountKey is the subset of a Google service-account JSON key file
// this package needs.
type serviceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// ServiceAccountTokenSource mints OAuth2 access tokens from a service
// account key via the RS256 JWT bearer grant. Minted tokens are cached
// in-memory until shortly before they expire.
type ServiceAccountTokenSource struct {
	email      string
	privateKey *rsa.PrivateKey
	httpClient *http.Client

	mu          sync.Mutex
	tokenURL    string
	cachedToken string
	expiry      time.Time
}

// NewServiceAccountTokenSource parses a service-account JSON key (as
// produced by the Google Cloud console: must contain at least
// client_email and private_key) and returns a TokenSource that mints
// access tokens on demand.
func NewServiceAccountTokenSource(keyJSON []byte) (*ServiceAccountTokenSource, error) {
	var key serviceAccountKey
	if err := json.Unmarshal(keyJSON, &key); err != nil {
		return nil, fmt.Errorf("gauth: parse service account key: %w", err)
	}
	if key.ClientEmail == "" {
		return nil, errors.New("gauth: service account key missing client_email")
	}
	if key.PrivateKey == "" {
		return nil, errors.New("gauth: service account key missing private_key")
	}

	priv, err := parsePrivateKey(key.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("gauth: parse private key: %w", err)
	}

	tokenURL := key.TokenURI
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	} else if err := validateTokenURL(tokenURL); err != nil {
		return nil, fmt.Errorf("gauth: %w", err)
	}

	return &ServiceAccountTokenSource{
		email:      key.ClientEmail,
		privateKey: priv,
		httpClient: http.DefaultClient,
		tokenURL:   tokenURL,
	}, nil
}

// validateTokenURL rejects any token_uri that is not an absolute https://
// URL. The token endpoint URL comes straight from the (untrusted) service
// -account key JSON and is used both as the POST target for the JWT bearer
// grant and as the JWT's "aud" claim; a tampered key with an http://,
// relative, or empty token_uri could redirect the signed assertion to an
// arbitrary or plaintext host. Real Google-issued keys always carry an
// https token_uri, so this rejects only malformed/tampered input.
func validateTokenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid token_uri %q: %w", raw, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("token_uri must be an absolute https:// URL, got %q", raw)
	}
	return nil
}

// NewServiceAccountTokenSourceFromFile reads and parses a service-account
// JSON key file at path.
func NewServiceAccountTokenSourceFromFile(path string) (*ServiceAccountTokenSource, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gauth: read service account key file: %w", err)
	}
	return NewServiceAccountTokenSource(b)
}

// SetTokenURL overrides the OAuth2 token endpoint URL. Intended for tests;
// production callers rely on the key's token_uri (or the Google default).
func (s *ServiceAccountTokenSource) SetTokenURL(u string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenURL = u
}

// parsePrivateKey decodes a PEM-encoded RSA private key, trying PKCS8
// first (the format Google issues service-account keys in) and falling
// back to PKCS1.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is %T, want *rsa.PrivateKey", key)
		}
		return rsaKey, nil
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, errors.New("unable to parse private key as PKCS8 or PKCS1")
}

func b64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// buildJWT constructs and RS256-signs the JWT assertion for the OAuth2 JWT
// bearer grant (https://tools.ietf.org/html/rfc7523), targeting aud with a
// 1-hour lifetime starting at now.
func (s *ServiceAccountTokenSource) buildJWT(now time.Time, aud string) (string, error) {
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}

	claims := map[string]any{
		"iss":   s.email,
		"scope": cloudPlatformScope,
		"aud":   aud,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	signingInput := b64URL(headerJSON) + "." + b64URL(claimsJSON)
	hashed := sha256.Sum256([]byte(signingInput))
	// Pass crypto/rand.Reader (not nil) so PKCS#1 v1.5 signing gets blinding,
	// which mitigates timing side-channels against the service-account
	// private key; a nil RNG disables that protection.
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	return signingInput + "." + b64URL(sig), nil
}

// TokenEndpointError is returned when Google's OAuth2 token endpoint
// responds with a non-2xx status. It implements IsRetryable() bool so that
// callers using internal/retry.Do (which checks for that method via
// errors.As against the retry.Retryable interface, not any concrete error
// type) correctly retry transient failures such as 5xx, 429, or 408
// responses from the token endpoint.
//
// gauth deliberately does not import the ai package to build an
// *ai.APICallError here: ai already imports internal/retry, and gauth is
// imported by providers/vertex, which imports ai -- so ai importing gauth
// back would risk introducing a cycle for no benefit. Since retry.Do only
// requires the IsRetryable() bool method (structural typing via
// errors.As), a small gauth-local error type is the cleanest cycle-free
// way to make token-endpoint failures classify correctly.
type TokenEndpointError struct {
	StatusCode int
	URL        string
	Body       string
}

// Error implements the error interface.
func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("gauth: token endpoint %s returned status %d: %s", e.URL, e.StatusCode, e.Body)
}

// IsRetryable reports whether the failure is likely transient. This
// mirrors ai.NewAPICallError's classification: 429, 408, and 5xx are
// retryable.
func (e *TokenEndpointError) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode == 408 || e.StatusCode >= 500
}

type tokenEndpointResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// Token returns a cached access token if one is still valid, otherwise
// mints a new one via the JWT bearer grant and caches it until
// expiry-60s.
//
// The mutex is held across the entire refresh (JWT build + token-endpoint
// round trip), not just the cache check, so concurrent callers on a cold or
// expired cache single-flight onto one refresh: only the first caller to
// acquire the lock actually mints a token; every other caller blocks on the
// lock and then finds a freshly populated, still-valid cache entry once it
// acquires the lock (the "double-check" below), returning that instead of
// making its own request.
func (s *ServiceAccountTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cachedToken != "" && time.Now().Before(s.expiry) {
		return s.cachedToken, nil
	}
	tokenURL := s.tokenURL

	now := time.Now()
	assertion, err := s.buildJWT(now, tokenURL)
	if err != nil {
		return "", fmt.Errorf("gauth: build JWT: %w", err)
	}

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("gauth: build token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gauth: token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gauth: read token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &TokenEndpointError{StatusCode: resp.StatusCode, URL: tokenURL, Body: string(body)}
	}

	var tr tokenEndpointResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("gauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("gauth: token response missing access_token")
	}

	s.cachedToken = tr.AccessToken
	s.expiry = now.Add(time.Duration(tr.ExpiresIn)*time.Second - expiryLeeway)

	return tr.AccessToken, nil
}
