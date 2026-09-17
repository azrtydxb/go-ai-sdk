// Package codexauth implements OpenAI Codex (ChatGPT Plus/Pro) OAuth2 flows:
// PKCE browser login, device-code login, token exchange, and token refresh.
//
// The package produces [Credential] values that contain an access token, a
// refresh token, an expiry time, and the account ID extracted from the
// access-token JWT. These credentials are deliberately NOT conflated with
// OpenAI API-key credentials (which use a simple "sk-..." string and the
// api.openai.com host).
package codexauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ----- constants -----

const (
	openAIClientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIAuthBaseURL           = "https://auth.openai.com"
	openAITokenURL              = openAIAuthBaseURL + "/oauth/token"
	openAIDeviceUserCodeURL     = openAIAuthBaseURL + "/api/accounts/deviceauth/usercode"
	openAIDeviceTokenURL        = openAIAuthBaseURL + "/api/accounts/deviceauth/token"
	openAIDeviceVerificationURI = openAIAuthBaseURL + "/codex/device"
	openAIDeviceRedirectURI     = openAIAuthBaseURL + "/deviceauth/callback"
	openAICodexBrowserCallback  = "http://localhost:1455/auth/callback"
	openAIScope                 = "openid profile email offline_access"
	jwtClaimPath                = "https://api.openai.com/auth"
	expiryLeeway                = 60 * time.Second
)

// Test override endpoints: set these in test init functions to point at
// fixture servers. Production code never sets them.
var (
	testTokenURL          string
	testDeviceTokenURL    string
	testDeviceUserCodeURL string
)

// Credential holds the OAuth tokens and the account ID for a Codex user.
type Credential struct {
	Access    string    // JWT access token (Bearer value)
	Refresh   string    // opaque refresh token
	Expires   time.Time // when Access is considered expired
	AccountID string    // chatgpt account ID from JWT claim
}

// Valid reports whether c has a non-empty access token that has not yet
// expired (within expiryLeeway).
func (c Credential) Valid() bool {
	return c.Access != "" && time.Now().Before(c.Expires)
}

// SaveCredential writes c to path with owner-only permissions. Parent
// directories are created with mode 0700, and the credential file is written
// with mode 0600 so OAuth refresh tokens are not world-readable.
func SaveCredential(path string, c Credential) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("codexauth: create credential directory: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("codexauth: marshal credential: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("codexauth: write credential: %w", err)
	}
	return nil
}

// LoadCredential reads a credential previously written by SaveCredential.
func LoadCredential(path string) (Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: read credential: %w", err)
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return Credential{}, fmt.Errorf("codexauth: decode credential: %w", err)
	}
	return c, nil
}

// generatePKCE produces a PKCE code-verifier and code-challenge pair.
func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = io.ReadFull(rand.Reader, raw); err != nil {
		return "", "", fmt.Errorf("codexauth: generate random verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge, nil
}

// generateState returns a random 16-byte hex string for CSRF prevention.
func generateState() string {
	raw := make([]byte, 16)
	_, _ = io.ReadFull(rand.Reader, raw)
	return fmt.Sprintf("%x", raw)
}

// decodeJWT peels the base64url-encoded payload out of a JWT and
// unmarshals it as a map. It does not verify the signature.
func decodeJWT(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("codexauth: invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("codexauth: decode JWT payload: %w", err)
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("codexauth: unmarshal JWT payload: %w", err)
	}
	return claims, nil
}

// extractAccountID peels the chatgpt_account_id from the JWT claim map.
func extractAccountID(token string) (string, error) {
	claims, err := decodeJWT(token)
	if err != nil {
		return "", fmt.Errorf("codexauth: decode access token: %w", err)
	}
	claimMap, ok := claims[jwtClaimPath].(map[string]any)
	if !ok {
		return "", fmt.Errorf("codexauth: missing claim %q in JWT", jwtClaimPath)
	}
	accountID, ok := claimMap["chatgpt_account_id"].(string)
	if !ok {
		return "", fmt.Errorf("codexauth: missing chatgpt_account_id in claim %q", jwtClaimPath)
	}
	return accountID, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

func tokenPost(ctx context.Context, client *http.Client, endpoint, formBody string) (*http.Response, error) {
	target := endpoint
	if testTokenURL != "" && strings.Contains(endpoint, "/oauth/token") {
		target = testTokenURL
	}
	if testDeviceTokenURL != "" && strings.Contains(endpoint, "/deviceauth/token") {
		target = testDeviceTokenURL
	}
	if testDeviceUserCodeURL != "" && strings.Contains(endpoint, "/deviceauth/usercode") {
		target = testDeviceUserCodeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(formBody))
	if err != nil {
		return nil, fmt.Errorf("codexauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return client.Do(req)
}

func credentialFromTokenResponse(ctx context.Context, client *http.Client, code, verifier string) (Credential, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openAIClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", openAICodexBrowserCallback)

	resp, err := tokenPost(ctx, client, openAITokenURL, form.Encode())
	if err != nil {
		return Credential{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credential{}, fmt.Errorf("codexauth: token exchange returned status %d: %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Credential{}, fmt.Errorf("codexauth: decode token response: %w", err)
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" || tr.ExpiresIn == 0 {
		return Credential{}, fmt.Errorf("codexauth: token response missing required fields")
	}

	accountID, err := extractAccountID(tr.AccessToken)
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: extract account ID: %w", err)
	}

	return Credential{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - expiryLeeway),
		AccountID: accountID,
	}, nil
}

func refreshTokenResponse(ctx context.Context, client *http.Client, refreshToken string) (Credential, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", openAIClientID)

	resp, err := tokenPost(ctx, client, openAITokenURL, form.Encode())
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: read refresh response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credential{}, fmt.Errorf("codexauth: token refresh returned status %d: %s", resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return Credential{}, fmt.Errorf("codexauth: decode refresh response: %w", err)
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" || tr.ExpiresIn == 0 {
		return Credential{}, fmt.Errorf("codexauth: refresh response missing required fields")
	}

	accountID, err := extractAccountID(tr.AccessToken)
	if err != nil {
		return Credential{}, fmt.Errorf("codexauth: extract account ID: %w", err)
	}

	return Credential{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - expiryLeeway),
		AccountID: accountID,
	}, nil
}

// BrowserLoginResult holds the data produced by a browser login.
type BrowserLoginResult struct {
	LoginURL     string // Full authorize URL to open in a browser.
	CallbackURL  string // localhost URL the browser redirects back to.
	State        string // CSRF state to verify in the callback.
	CodeVerifier string // PKCE verifier to pass to ExchangeAuthorizationCode.
}

// StartBrowserLogin builds the authorize URL and returns the data needed to
// open it in a browser and exchange the code once the browser returns.
func StartBrowserLogin() BrowserLoginResult {
	verifier, challenge, _ := generatePKCE()
	state := generateState()

	u := url.URL{
		Scheme: "https",
		Host:   "auth.openai.com",
		Path:   "/oauth/authorize",
	}
	q := u.Query()
	q.Set("client_id", openAIClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", openAICodexBrowserCallback)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", openAIScope)
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "go-ai-sdk")
	u.RawQuery = q.Encode()

	return BrowserLoginResult{
		LoginURL:     u.String(),
		CallbackURL:  openAICodexBrowserCallback,
		State:        state,
		CodeVerifier: verifier,
	}
}

// ExchangeAuthorizationCode exchanges an authorization code (received from
// the OAuth callback) for a Credential.
func ExchangeAuthorizationCode(ctx context.Context, client *http.Client, code, callbackState, verifier string) (Credential, error) {
	if verifier == "" {
		return Credential{}, fmt.Errorf("codexauth: code verifier must not be empty")
	}
	return credentialFromTokenResponse(ctx, client, code, verifier)
}

// DeviceCodeResult holds the data returned when a device code is issued.
type DeviceCodeResult struct {
	UserCode        string    // human-readable code for the user
	DeviceCode      string    // opaque code for polling
	VerificationURI string    // URL to visit for authorization
	IntervalSeconds int       // seconds between polls
	ExpiresAt       time.Time // when the device code expires
}

// StartDeviceCode requests a device code from OpenAI.
func StartDeviceCode(ctx context.Context, client *http.Client) (DeviceCodeResult, error) {
	payload, _ := json.Marshal(map[string]string{"client_id": openAIClientID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDeviceUserCodeURL, strings.NewReader(string(payload)))
	if err != nil {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: build device request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	target := openAIDeviceUserCodeURL
	if testDeviceUserCodeURL != "" {
		target = testDeviceUserCodeURL
	}
	req.URL, _ = url.Parse(target)

	resp, err := client.Do(req)
	if err != nil {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: device code request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: read device response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: device code request returned status %d: %s", resp.StatusCode, string(body))
	}

	// The OpenAI Codex API returns interval and expires_in as strings.
	var deviceResp struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     string `json:"interval"`
		ExpiresIn    string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: decode device response: %w", err)
	}
	if deviceResp.DeviceAuthID == "" || deviceResp.UserCode == "" {
		return DeviceCodeResult{}, fmt.Errorf("codexauth: device response missing required fields")
	}

	interval, _ := strconv.Atoi(deviceResp.Interval)
	if interval <= 0 {
		interval = 5
	}
	expiresIn, _ := strconv.Atoi(deviceResp.ExpiresIn)
	if expiresIn <= 0 {
		expiresIn = 900
	}

	return DeviceCodeResult{
		UserCode:        deviceResp.UserCode,
		DeviceCode:      deviceResp.DeviceAuthID,
		VerificationURI: openAIDeviceVerificationURI,
		IntervalSeconds: interval,
		ExpiresAt:       time.Now().Add(time.Duration(expiresIn) * time.Second),
	}, nil
}

// DeviceAuthStatus reports the result of a device-code poll.
type DeviceAuthStatus int

const (
	DeviceAuthPending  DeviceAuthStatus = iota // still waiting for user to authorize
	DeviceAuthComplete                         // user authorized; credentials returned
	DeviceAuthFailed                           // authorization failed
	DeviceAuthSlowDown                         // poll too fast; increase interval
)

// DeviceAuthPollResult wraps a poll result.
type DeviceAuthPollResult struct {
	Status   DeviceAuthStatus
	Cred     Credential
	Interval int // suggested poll interval (on slow_down only)
}

// PollDeviceCode sends one poll request for the given device code.
func PollDeviceCode(ctx context.Context, client *http.Client, deviceCode, userCode string) (DeviceAuthPollResult, error) {
	payload, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceCode,
		"user_code":      userCode,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDeviceTokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return DeviceAuthPollResult{}, fmt.Errorf("codexauth: build device poll: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	target := openAIDeviceTokenURL
	if testDeviceTokenURL != "" {
		target = testDeviceTokenURL
	}
	req.URL, _ = url.Parse(target)

	resp, err := client.Do(req)
	if err != nil {
		return DeviceAuthPollResult{}, fmt.Errorf("codexauth: device poll: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return DeviceAuthPollResult{}, fmt.Errorf("codexauth: read device poll response: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		var authResp struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
		}
		if err := json.Unmarshal(body, &authResp); err != nil {
			return DeviceAuthPollResult{}, fmt.Errorf("codexauth: decode device auth response: %w", err)
		}
		if authResp.AuthorizationCode == "" || authResp.CodeVerifier == "" {
			return DeviceAuthPollResult{}, fmt.Errorf("codexauth: device auth response missing required fields")
		}
		cred, err := credentialFromTokenResponse(ctx, client, authResp.AuthorizationCode, authResp.CodeVerifier)
		if err != nil {
			return DeviceAuthPollResult{Status: DeviceAuthFailed}, err
		}
		return DeviceAuthPollResult{Status: DeviceAuthComplete, Cred: cred}, nil
	}

	switch resp.StatusCode {
	case 400:
		var errResp struct {
			Error    string `json:"error"`
			Interval string `json:"interval"`
		}
		_ = json.Unmarshal(body, &errResp)
		if errResp.Error == "slow_down" {
			interval, _ := strconv.Atoi(errResp.Interval)
			if interval <= 0 {
				interval = 5
			}
			return DeviceAuthPollResult{Status: DeviceAuthSlowDown, Interval: interval}, nil
		}
		interval, _ := strconv.Atoi(errResp.Interval)
		if interval <= 0 {
			interval = 5
		}
		return DeviceAuthPollResult{Status: DeviceAuthPending, Interval: interval}, nil
	case 403, 404:
		return DeviceAuthPollResult{Status: DeviceAuthPending, Interval: 5}, nil
	default:
		return DeviceAuthPollResult{Status: DeviceAuthFailed}, fmt.Errorf("codexauth: device poll returned status %d: %s", resp.StatusCode, string(body))
	}
}

// LoginDevice waits for the user to authorize the device code, polling at
// the server's pace. Returns a Credential on completion.
func LoginDevice(ctx context.Context, client *http.Client, dc DeviceCodeResult) (Credential, error) {
	deadline := dc.ExpiresAt
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return Credential{}, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return Credential{}, ctx.Err()
		case <-time.After(time.Duration(dc.IntervalSeconds) * time.Second):
		}
		result, err := PollDeviceCode(ctx, client, dc.DeviceCode, dc.UserCode)
		if err != nil {
			return Credential{}, err
		}
		switch result.Status {
		case DeviceAuthComplete:
			return result.Cred, nil
		case DeviceAuthSlowDown:
			dc.IntervalSeconds = result.Interval
		case DeviceAuthFailed:
			return Credential{}, fmt.Errorf("codexauth: device authorization denied")
		}
	}
	return Credential{}, fmt.Errorf("codexauth: device code expired")
}

// RefreshToken exchanges the provided refresh token for a new access/refresh
// pair, returning a Credential with an updated expiry and account ID.
func RefreshToken(ctx context.Context, client *http.Client, refreshToken string) (Credential, error) {
	return refreshTokenResponse(ctx, client, refreshToken)
}
