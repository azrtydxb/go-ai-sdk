// Package anthropicauth mints Anthropic Messages API access tokens from a
// Claude Pro/Max subscription via PKCE OAuth, following the flow documented
// at claude.ai/oauth/authorize and platform.claude.com/v1/oauth/token.
//
// The package exposes a TokenSource that mirrors the pattern used by
// internal/gauth: an in-memory cached bearer token, refreshed on expiry via
// PKCE authorization-code exchange or refresh_token grant.
package anthropicauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/oauthflow"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	// clientID is the Anthropic web-app client ID used for PKCE flows on
	// claude.ai. This is a public value from Anthropic's own client.
	clientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// authorizeURL is the Anthropic OAuth authorize endpoint.
	authorizeURL = "https://claude.ai/oauth/authorize"

	// tokenURL is the Anthropic OAuth token endpoint.
	tokenURL = "https://platform.claude.com/v1/oauth/token"

	// scopes lists the OAuth scopes required for full Claude Pro/Max access.
	scopes = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"

	// callbackPort is the local port the callback HTTP server listens on.
	callbackPort = 53692

	// callbackPath is the callback URL path.
	callbackPath = "/callback"

	// callbackHost is the callback listener bind address.
	callbackHost = "127.0.0.1"

	// expiryLeeway is subtracted from a minted token's reported lifetime so
	// cached tokens are refreshed slightly before they actually expire.
	expiryLeeway = 60 * time.Second
)

// redirectURI returns the OAuth callback redirect URI used by this package.
func redirectURI() string {
	return fmt.Sprintf("http://localhost:%d%s", callbackPort, callbackPath)
}

// ---------------------------------------------------------------------------
// PKCE helpers
// ---------------------------------------------------------------------------

// generatePKCE produces a random PKCE code verifier and its S256 challenge.
func generatePKCE() (verifier, challenge string) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		panic("anthropicauth: crypto/rand failed: " + err.Error())
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)

	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return
}

// ---------------------------------------------------------------------------
// Token and credential types
// ---------------------------------------------------------------------------

// Credentials holds the three fields returned by the Anthropic OAuth token
// endpoint after an authorization-code exchange.
type Credentials struct {
	// Access is the bearer access token used in the Authorization header.
	Access string
	// Refresh is the opaque refresh token used to obtain a new access token
	// once the current one expires.
	Refresh string
	// Expires is the wall-clock time at which the access token is considered
	// expired (now + expires_in - leeway).
	Expires time.Time
}

// TokenSource yields a bearer access token for the Anthropic Messages API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticTokenSource is a TokenSource that always returns its token.
type StaticTokenSource string

// Token returns the static access token. It never errors.
func (s StaticTokenSource) Token(context.Context) (string, error) {
	return string(s), nil
}

// OAuthTokenSource obtains and caches an access token from Anthropic's OAuth
// token endpoint using a PKCE authorization-code flow, then refreshes
// automatically using the refresh_token grant.
type OAuthTokenSource struct {
	httpClient *http.Client

	mu           sync.Mutex
	cachedToken  string
	expiry       time.Time
	refreshToken string
}

// NewOAuthTokenSource creates a TokenSource ready to start the PKCE flow.
// Call CompletePKCE to finish the login handshake.
func NewOAuthTokenSource() *OAuthTokenSource {
	return &OAuthTokenSource{httpClient: http.DefaultClient}
}

// NewOAuthTokenSourceWithCredentials creates a TokenSource from credentials
// loaded from storage or returned by CompletePKCE.
func NewOAuthTokenSourceWithCredentials(creds Credentials) *OAuthTokenSource {
	return &OAuthTokenSource{
		httpClient:   http.DefaultClient,
		cachedToken:  creds.Access,
		expiry:       creds.Expires,
		refreshToken: creds.Refresh,
	}
}

// Credentials returns a snapshot of the currently cached OAuth credentials.
func (t *OAuthTokenSource) Credentials() Credentials {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Credentials{Access: t.cachedToken, Refresh: t.refreshToken, Expires: t.expiry}
}

// SaveCredentials writes creds to path with owner-only permissions. Parent
// directories are created with mode 0700, and the credential file is written
// with mode 0600 so OAuth refresh tokens are not world-readable.
func SaveCredentials(path string, creds Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("anthropicauth: create credential directory: %w", err)
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("anthropicauth: marshal credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("anthropicauth: write credentials: %w", err)
	}
	return nil
}

// LoadCredentials reads credentials previously written by SaveCredentials.
func LoadCredentials(path string) (Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("anthropicauth: read credentials: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("anthropicauth: decode credentials: %w", err)
	}
	return creds, nil
}

// SetHTTPClient overrides the *http.Client used for requests.
func (t *OAuthTokenSource) SetHTTPClient(c *http.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.httpClient = c
}

// Token returns a cached access token if one is still valid, otherwise
// performs a refresh_token grant if a refresh token is available.
//
// If no refresh token has been seen yet (the flow was not completed via
// CompletePKCE), Token returns an error directing the caller to run the PKCE
// flow.
func (t *OAuthTokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cachedToken != "" && time.Now().Before(t.expiry) {
		return t.cachedToken, nil
	}

	if t.refreshToken == "" {
		return "", errNoRefreshToken
	}

	return t.doRefresh(ctx)
}

var errNoRefreshToken = errors.New("anthropicauth: no refresh token — call CompletePKCE first")

// CompletePKCE performs the PKCE authorization-code login flow: it starts a
// local HTTP server on localhost to receive the OAuth callback, launches the
// authorize URL in the system browser (or prompts for manual code input),
// waits for the user to complete the login, exchanges the authorization code
// for tokens, and stores the result.
//
// The interaction argument supplies the browser-open and prompt capabilities.
// Returns the Credentials (access, refresh, expires) on success.
func (t *OAuthTokenSource) CompletePKCE(ctx context.Context, inter *Interaction) (*Credentials, error) {
	ctx, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if inter == nil {
		return nil, errors.New("anthropicauth: interaction required")
	}
	verifier, challenge := generatePKCE()
	expectedState, _ := generatePKCE()

	// Start the callback server.
	srv, authURL, cancel, err := startCallbackServer(expectedState, challenge)
	var results <-chan oauthflow.Result
	if err != nil && inter.Prompt == nil {
		return nil, oauthflow.ErrCallbackUnavailable
	}
	if err == nil {
		defer cancel()
		results = srv.resultCh
	}

	// Open browser to authorize URL.
	if inter.OpenURL != nil {
		if err := inter.OpenURL(ctx, authURL); err != nil {
			return nil, fmt.Errorf("anthropicauth: open browser: %w", err)
		}
	}

	// Print a message so the user knows what to do.
	if inter.Stderr != nil {
		fmt.Fprintf(inter.Stderr(), "anthropicauth: please complete login in your browser.\nWaiting for callback on %s\n", authURL)
	}

	result, err := oauthflow.Wait(ctx, results, inter.Prompt,
		"Paste the full callback URL or code#state:\n"+authURL, expectedState)
	if err != nil {
		return nil, err
	}

	// Exchange the authorization code for tokens.
	creds, err := t.exchangeCode(ctx, result.Code, result.State, verifier, challenge)
	if err != nil {
		return nil, fmt.Errorf("anthropicauth: exchange code: %w", err)
	}
	return creds, nil
}

// parseAuthInput tries to extract the authorization code from user input.
// The input may be a plain code, a redirect URL, or a URL with #fragment.
func parseAuthInput(input, _ string) (code, state string) {
	result := oauthflow.Parse(input)
	return result.Code, result.State
}

// exchangeCode performs the token exchange: POST to the token endpoint with
// the authorization code, state, and PKCE verifier.
func (t *OAuthTokenSource) exchangeCode(ctx context.Context, code, state, verifier, _ string) (*Credentials, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	payload, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI(),
		"code_verifier": verifier,
	})
	if err != nil {
		return nil, fmt.Errorf("anthropicauth: marshal token request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("anthropicauth: build token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropicauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropicauth: read token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &TokenEndpointError{StatusCode: resp.StatusCode, URL: tokenURL, Body: string(body)}
	}

	var tr tokenResponse
	if err := jsonDecode(body, &tr); err != nil {
		return nil, fmt.Errorf("anthropicauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, errors.New("anthropicauth: token response missing access_token")
	}

	now := time.Now()
	t.cachedToken = tr.AccessToken
	t.refreshToken = tr.RefreshToken
	t.expiry = now.Add(time.Duration(tr.ExpiresIn)*time.Second - expiryLeeway)

	return &Credentials{
		Access:  tr.AccessToken,
		Refresh: tr.RefreshToken,
		Expires: t.expiry,
	}, nil
}

// Refresh forces a refresh_token grant, obtaining a new access token.
// This is called internally by Token on cache miss; callers may also invoke
// it directly to proactively refresh before expiry.
func (t *OAuthTokenSource) Refresh(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.refreshToken == "" {
		return errNoRefreshToken
	}
	_, err := t.doRefresh(ctx)
	return err
}

func (t *OAuthTokenSource) doRefresh(ctx context.Context) (string, error) {
	payload, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     clientID,
		"refresh_token": t.refreshToken,
	})
	if err != nil {
		return "", fmt.Errorf("anthropicauth: marshal refresh request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return "", fmt.Errorf("anthropicauth: build refresh request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("anthropicauth: refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("anthropicauth: read refresh response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &TokenEndpointError{StatusCode: resp.StatusCode, URL: tokenURL, Body: string(body)}
	}

	var tr tokenResponse
	if err := jsonDecode(body, &tr); err != nil {
		return "", fmt.Errorf("anthropicauth: decode refresh response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", errors.New("anthropicauth: refresh response missing access_token")
	}

	now := time.Now()
	t.cachedToken = tr.AccessToken
	if tr.RefreshToken != "" {
		t.refreshToken = tr.RefreshToken
	}
	t.expiry = now.Add(time.Duration(tr.ExpiresIn)*time.Second - expiryLeeway)

	return t.cachedToken, nil
}

// ---------------------------------------------------------------------------
// Callback server
// ---------------------------------------------------------------------------

type callbackResult = oauthflow.Result

type callbackServer struct {
	server   *http.Server
	resultCh chan callbackResult
}

func (cs *callbackServer) waitForCode(ctx context.Context) (*callbackResult, error) {
	select {
	case result := <-cs.resultCh:
		return &result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var listenCallback = net.Listen

func startCallbackServer(expectedState, challenge string) (*callbackServer, string, func(), error) {
	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, oauthflow.Handler(expectedState, resultCh))

	// Build the authorize URL.
	authParams := url.Values{}
	authParams.Set("response_type", "code")
	authParams.Set("client_id", clientID)
	authParams.Set("redirect_uri", redirectURI())
	authParams.Set("scope", scopes)
	authParams.Set("state", expectedState)
	authParams.Set("code_challenge", challenge)
	authParams.Set("code_challenge_method", "S256")

	authURL := authorizeURL + "?" + authParams.Encode()
	ln, err := listenCallback("tcp", fmt.Sprintf("%s:%d", callbackHost, callbackPort))
	if err != nil {
		return nil, authURL, nil, err
	}
	srv := &http.Server{Addr: ln.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ln) }()

	cancel := func() {
		_ = srv.Close()
		_ = ln.Close()
		<-done
	}
	return &callbackServer{server: srv, resultCh: resultCh}, authURL, cancel, nil
}

// ---------------------------------------------------------------------------
// Interaction interface
// ---------------------------------------------------------------------------

// Interaction abstracts browser launch and CLI prompts so the library can run
// in both interactive (human-in-the-loop) and automated (headless) contexts.
type Interaction struct {
	// OpenURL opens a browser or otherwise makes the URL visible to the user.
	OpenURL func(ctx context.Context, url string) error

	// Stderr is where diagnostic messages are written. If nil, output is
	// suppressed.
	Stderr func() io.Writer

	// Prompt is called when the user cannot complete the login via browser
	// (e.g. headless). It should return the authorization code or full
	// redirect URL including state, or code#state. It must return when ctx is
	// canceled. If nil, only the browser callback path is supported.
	Prompt func(ctx context.Context, message string) (string, error)
}

// NewInteractiveInteraction returns an Interaction that opens the system
// browser and waits for the local callback. Use it when running interactively.
func NewInteractiveInteraction() *Interaction {
	return &Interaction{
		OpenURL: openBrowser,
		Stderr:  func() io.Writer { return os.Stderr },
		Prompt:  nil,
	}
}

// openBrowser opens the given URL in the system's default browser.
func openBrowser(ctx context.Context, urlStr string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", urlStr)
	default:
		cmd = exec.CommandContext(ctx, "open", urlStr)
	}
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Error types
// ---------------------------------------------------------------------------

// TokenEndpointError is returned when Anthropic's OAuth token endpoint
// responds with a non-2xx status. It implements IsRetryable() bool so that
// callers using internal/retry.Do correctly retry transient failures.
type TokenEndpointError struct {
	StatusCode int
	URL        string
	Body       string
}

// Error implements the error interface.
func (e *TokenEndpointError) Error() string {
	return fmt.Sprintf("anthropicauth: token endpoint %s returned status %d: %s", e.URL, e.StatusCode, e.Body)
}

// IsRetryable reports whether the failure is likely transient. 429, 408,
// and 5xx are retryable.
func (e *TokenEndpointError) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode == 408 || e.StatusCode >= 500
}

// ---------------------------------------------------------------------------
// Wire types
// ---------------------------------------------------------------------------

// tokenResponse is the JSON body returned by Anthropic's token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// jsonDecode unmarshals JSON into v, returning an error on failure.
func jsonDecode(body []byte, v any) error {
	return decodeJSON(body, v)
}

// decodeJSON is a function var so tests can swap in a mock decoder.
var decodeJSON = func(body []byte, v any) error {
	return json.Unmarshal(body, v)
}
