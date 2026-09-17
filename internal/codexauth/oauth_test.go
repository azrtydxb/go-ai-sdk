package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeJWT(claims map[string]any) string {
	header := `{"alg":"none","typ":"JWT"}`
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))
	claimsBytes, _ := json.Marshal(claims)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)
	return headerB64 + "." + claimsB64 + "."
}

func TestExtractAccountID(t *testing.T) {
	token := fakeJWT(map[string]any{
		"sub":        "user123",
		jwtClaimPath: map[string]any{"chatgpt_account_id": "acc-test-42"},
	})
	id, err := extractAccountID(token)
	if err != nil {
		t.Fatalf("extractAccountID: %v", err)
	}
	if id != "acc-test-42" {
		t.Errorf("accountId = %q, want %q", id, "acc-test-42")
	}
}

func TestExtractAccountID_MissingClaim(t *testing.T) {
	token := fakeJWT(map[string]any{"sub": "user123"})
	_, err := extractAccountID(token)
	if err == nil {
		t.Fatal("expected error for missing claim, got nil")
	}
}

func TestExtractAccountID_MissingAccountID(t *testing.T) {
	token := fakeJWT(map[string]any{jwtClaimPath: map[string]any{"other": "val"}})
	_, err := extractAccountID(token)
	if err == nil {
		t.Fatal("expected error for missing chatgpt_account_id, got nil")
	}
}

func TestSaveLoadCredentialRestrictivePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "codex.json")
	cred := Credential{Access: "access", Refresh: "refresh", Expires: time.Now().Add(time.Hour), AccountID: "acct"}

	if err := SaveCredential(path, cred); err != nil {
		t.Fatalf("SaveCredential: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credential: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("credential mode = %o, want 600", got)
	}

	loaded, err := LoadCredential(path)
	if err != nil {
		t.Fatalf("LoadCredential: %v", err)
	}
	if loaded.Access != cred.Access || loaded.Refresh != cred.Refresh || loaded.AccountID != cred.AccountID {
		t.Fatalf("loaded credential = %+v, want %+v", loaded, cred)
	}
}

func TestCredential_Valid(t *testing.T) {
	c := Credential{Access: "fake", Refresh: "fake", Expires: time.Now().Add(time.Hour)}
	if !c.Valid() {
		t.Error("Valid() = false, want true")
	}
	c.Access = ""
	if c.Valid() {
		t.Error("Valid() = true with empty access")
	}
	c.Access = "fake"
	c.Expires = time.Now().Add(-time.Hour)
	if c.Valid() {
		t.Error("Valid() = true with past expiry")
	}
}

func TestGeneratePKCE(t *testing.T) {
	v1, c1, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if v1 == "" || c1 == "" || v1 == c1 {
		t.Fatal("bad PKCE pair")
	}
	v2, _, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if v1 == v2 {
		t.Error("two PKCE pairs have the same verifier")
	}
}

func TestStartBrowserLogin(t *testing.T) {
	result := StartBrowserLogin()
	if result.LoginURL == "" {
		t.Error("LoginURL is empty")
	}
	if !strings.HasPrefix(result.LoginURL, "https://auth.openai.com/oauth/authorize?") {
		t.Errorf("LoginURL = %q, want https://auth.openai.com/oauth/authorize?", result.LoginURL)
	}
	if result.CallbackURL != openAICodexBrowserCallback {
		t.Errorf("CallbackURL = %q, want %q", result.CallbackURL, openAICodexBrowserCallback)
	}
	if result.State == "" || result.CodeVerifier == "" {
		t.Error("State or CodeVerifier is empty")
	}
	u, err := url.Parse(result.LoginURL)
	if err != nil {
		t.Fatalf("parse LoginURL: %v", err)
	}
	q := u.Query()
	if q.Get("id_token_add_organizations") != "true" {
		t.Errorf("id_token_add_organizations = %q, want true", q.Get("id_token_add_organizations"))
	}
	if q.Get("codex_cli_simplified_flow") != "true" {
		t.Errorf("codex_cli_simplified_flow = %q, want true", q.Get("codex_cli_simplified_flow"))
	}
	if q.Get("originator") != "go-ai-sdk" {
		t.Errorf("originator = %q, want go-ai-sdk", q.Get("originator"))
	}
}

func TestExchangeAuthorizationCode(t *testing.T) {
	fakeClaims := map[string]any{
		"sub":        "user123",
		jwtClaimPath: map[string]any{"chatgpt_account_id": "acc-fake"},
	}
	fakeToken := fakeJWT(fakeClaims)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "authorization_code" {
			t.Error("grant_type is not authorization_code")
		}
		resp := map[string]any{
			"access_token":  fakeToken,
			"refresh_token": "fake-refresh",
			"expires_in":    3600,
			"token_type":    "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testTokenURL
	testTokenURL = server.URL
	defer func() { testTokenURL = orig }()

	ctx := context.Background()
	cred, err := ExchangeAuthorizationCode(ctx, server.Client(), "fake-code", "fake-state", "fake-verifier")
	if err != nil {
		t.Fatalf("ExchangeAuthorizationCode: %v", err)
	}
	if cred.Access != fakeToken {
		t.Errorf("Access = %q, want %q", cred.Access, fakeToken)
	}
	if cred.Refresh != "fake-refresh" {
		t.Errorf("Refresh = %q, want %q", cred.Refresh, "fake-refresh")
	}
	if cred.AccountID != "acc-fake" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-fake")
	}
	if !cred.Valid() {
		t.Error("Credential is not valid")
	}
}

func TestExchangeAuthorizationCode_BadResponse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testTokenURL
	testTokenURL = server.URL
	defer func() { testTokenURL = orig }()

	ctx := context.Background()
	_, err := ExchangeAuthorizationCode(ctx, server.Client(), "fake-code", "fake-state", "fake-verifier")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
}

func TestRefreshToken(t *testing.T) {
	fakeClaims := map[string]any{
		"sub":        "user456",
		jwtClaimPath: map[string]any{"chatgpt_account_id": "acc-refresh"},
	}
	fakeToken := fakeJWT(fakeClaims)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		if form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", form.Get("grant_type"))
		}
		resp := map[string]any{
			"access_token":  fakeToken,
			"refresh_token": "new-refresh",
			"expires_in":    7200,
			"token_type":    "Bearer",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testTokenURL
	testTokenURL = server.URL
	defer func() { testTokenURL = orig }()

	ctx := context.Background()
	cred, err := RefreshToken(ctx, server.Client(), "my-refresh")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if cred.Access != fakeToken {
		t.Errorf("Access = %q, want %q", cred.Access, fakeToken)
	}
	if cred.AccountID != "acc-refresh" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-refresh")
	}
}

func TestStartDeviceCode(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"device_auth_id": "dev-auth-123",
			"user_code":      "ABCD-1234",
			"interval":       "5",
			"expires_in":     "900",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testDeviceUserCodeURL
	testDeviceUserCodeURL = server.URL
	defer func() { testDeviceUserCodeURL = orig }()

	ctx := context.Background()
	dc, err := StartDeviceCode(ctx, server.Client())
	if err != nil {
		t.Fatalf("StartDeviceCode: %v", err)
	}
	if dc.DeviceCode != "dev-auth-123" {
		t.Errorf("DeviceCode = %q, want %q", dc.DeviceCode, "dev-auth-123")
	}
	if dc.UserCode != "ABCD-1234" {
		t.Errorf("UserCode = %q, want %q", dc.UserCode, "ABCD-1234")
	}
	if dc.IntervalSeconds != 5 {
		t.Errorf("IntervalSeconds = %d, want 5", dc.IntervalSeconds)
	}
}

func TestPollDeviceCode_Complete(t *testing.T) {
	fakeClaims := map[string]any{
		"sub":        "user-poll",
		jwtClaimPath: map[string]any{"chatgpt_account_id": "acc-poll"},
	}
	fakeToken := fakeJWT(fakeClaims)

	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		switch {
		case callCount == 1:
			resp := map[string]any{
				"authorization_code": "auth-code-123",
				"code_verifier":      "verifier-456",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case callCount == 2:
			resp := map[string]any{
				"access_token":  fakeToken,
				"refresh_token": "final-refresh",
				"expires_in":    3600,
				"token_type":    "Bearer",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case callCount == 3:
			t.Error("unexpected third poll")
			w.WriteHeader(500)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	origD := testDeviceTokenURL
	origT := testTokenURL
	testDeviceTokenURL = server.URL
	testTokenURL = server.URL
	defer func() {
		testDeviceTokenURL = origD
		testTokenURL = origT
	}()

	ctx := context.Background()
	result, err := PollDeviceCode(ctx, server.Client(), "dev-auth-id", "USER-1234")
	if err != nil {
		t.Fatalf("PollDeviceCode: %v", err)
	}
	if result.Status != DeviceAuthComplete {
		t.Errorf("Status = %d, want %d", result.Status, DeviceAuthComplete)
	}
	if result.Cred.AccountID != "acc-poll" {
		t.Errorf("AccountID = %q, want %q", result.Cred.AccountID, "acc-poll")
	}
}

func TestPollDeviceCode_Pending(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// authorization_pending is returned with HTTP 400 by the real API
		w.WriteHeader(400)
		resp := map[string]any{
			"error":    "authorization_pending",
			"interval": "10",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testDeviceTokenURL
	testDeviceTokenURL = server.URL
	defer func() { testDeviceTokenURL = orig }()

	ctx := context.Background()
	result, err := PollDeviceCode(ctx, server.Client(), "dev-auth-id", "USER-1234")
	if err != nil {
		t.Fatalf("PollDeviceCode: %v", err)
	}
	if result.Status != DeviceAuthPending {
		t.Errorf("Status = %d, want %d", result.Status, DeviceAuthPending)
	}
	if result.Interval != 10 {
		t.Errorf("Interval = %d, want 10", result.Interval)
	}
}

func TestPollDeviceCode_SlowDown(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		resp := map[string]any{
			"error":    "slow_down",
			"interval": "15",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testDeviceTokenURL
	testDeviceTokenURL = server.URL
	defer func() { testDeviceTokenURL = orig }()

	ctx := context.Background()
	result, err := PollDeviceCode(ctx, server.Client(), "dev-auth-id", "USER-1234")
	if err != nil {
		t.Fatalf("PollDeviceCode: %v", err)
	}
	if result.Status != DeviceAuthSlowDown {
		t.Errorf("Status = %d, want %d", result.Status, DeviceAuthSlowDown)
	}
	if result.Interval != 15 {
		t.Errorf("Interval = %d, want 15", result.Interval)
	}
}

func TestPollDeviceCode_ForbiddenIsPending(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"deviceauth_authorization_pending"}`))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	orig := testDeviceTokenURL
	testDeviceTokenURL = server.URL
	defer func() { testDeviceTokenURL = orig }()

	ctx := context.Background()
	result, err := PollDeviceCode(ctx, server.Client(), "dev-auth-id", "USER-1234")
	if err != nil {
		t.Fatalf("PollDeviceCode: %v", err)
	}
	if result.Status != DeviceAuthPending {
		t.Errorf("Status = %d, want %d", result.Status, DeviceAuthPending)
	}
}
