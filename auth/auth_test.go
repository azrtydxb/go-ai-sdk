package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/auth"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func tokenClient(t *testing.T, provider string, inspect func(map[string]string)) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		wantHost := "auth.openai.com"
		if provider == "anthropic" {
			wantHost = "platform.claude.com"
		}
		if r.URL.Host != wantHost || r.Method != http.MethodPost {
			t.Errorf("unexpected token endpoint")
		}
		fields := map[string]string{}
		if provider == "anthropic" {
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Error(err)
			}
		} else {
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			for k := range r.Form {
				fields[k] = r.Form.Get(k)
			}
		}
		inspect(fields)
		access := "fixture-access"
		if provider == "codex" {
			access = "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fixture-account"}}`)) + ".sig"
		}
		body, _ := json.Marshal(map[string]any{"access_token": access, "refresh_token": "rotated-refresh", "expires_in": 3600})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
}

func TestLoginPublicManual(t *testing.T) {
	for _, provider := range []string{"codex", "anthropic"} {
		for _, mode := range []string{"manual-url", "manual-code"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var authorize *url.URL
				calls := 0
				client := tokenClient(t, provider, func(fields map[string]string) {
					calls++
					if fields["grant_type"] != "authorization_code" || fields["code"] != "fixture-code" {
						t.Error("wrong exchange")
					}
					verifier := fields["code_verifier"]
					hash := sha256.Sum256([]byte(verifier))
					if verifier == "" || verifier == authorize.Query().Get("state") || base64.RawURLEncoding.EncodeToString(hash[:]) != authorize.Query().Get("code_challenge") {
						t.Error("invalid or exposed PKCE verifier")
					}
					if provider == "anthropic" && fields["state"] != authorize.Query().Get("state") {
						t.Error("exchange lost independent state")
					}
				})
				promptDone := make(chan struct{})
				creds, err := auth.Login(ctx, provider, client, auth.Interaction{
					OpenURL: func(ctx context.Context, raw string) error {
						authorize, _ = url.Parse(raw)
						return nil
					},
					Prompt: func(ctx context.Context, _ string) (string, error) {
						defer close(promptDone)
						state := authorize.Query().Get("state")
						if mode == "manual-code" {
							return "fixture-code#" + state, nil
						}
						return authorize.Query().Get("redirect_uri") + "?code=fixture-code&state=" + url.QueryEscape(state), nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-promptDone:
				default:
					t.Error("losing prompt not joined")
				}
				if calls != 1 || creds.Access == "" || creds.Refresh != "rotated-refresh" || !creds.Expires.After(time.Now()) {
					t.Error("missing updated credentials")
				}
				if provider == "codex" && creds.AccountID != "fixture-account" {
					t.Error("missing account ID")
				}
			})
		}
	}
}

func TestLoginRejectsManualStateAndCancels(t *testing.T) {
	for _, provider := range []string{"codex", "anthropic"} {
		var previousState string
		for _, mode := range []string{"wrong-state", "missing-state", "stale-state", "cancel", "deadline"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				var authorize *url.URL
				client := tokenClient(t, provider, func(map[string]string) { t.Error("invalid login reached token endpoint") })
				_, err := auth.Login(ctx, provider, client, auth.Interaction{
					OpenURL: func(_ context.Context, raw string) error { authorize, _ = url.Parse(raw); return nil },
					Prompt: func(ctx context.Context, _ string) (string, error) {
						switch mode {
						case "cancel":
							cancel()
							<-ctx.Done()
							return "", ctx.Err()
						case "deadline":
							return "", context.DeadlineExceeded
						case "missing-state":
							return "fixture-code", nil
						case "stale-state":
							return "fixture-code#" + previousState, nil
						default:
							return "fixture-code#wrong", nil
						}
					},
				})
				if err == nil {
					t.Fatal("invalid login accepted")
				}
				if mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Error("cancellation identity lost")
				}
				if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
					t.Error("deadline identity lost")
				}
				if previousState == authorize.Query().Get("state") {
					t.Error("state reused between logins")
				}
				previousState = authorize.Query().Get("state")
			})
		}
	}
}

func TestRefreshPublicAndSafeErrors(t *testing.T) {
	for _, provider := range []string{"codex", "anthropic"} {
		t.Run(provider, func(t *testing.T) {
			current := auth.Credentials{Access: "old-access", Refresh: "old-refresh"}
			client := tokenClient(t, provider, func(fields map[string]string) {
				if fields["grant_type"] != "refresh_token" || fields["refresh_token"] != current.Refresh {
					t.Error("wrong refresh grant")
				}
			})
			updated, err := auth.Refresh(context.Background(), provider, client, current)
			if err != nil || updated.Access == current.Access || updated.Refresh != "rotated-refresh" || updated.Expires.IsZero() {
				t.Fatal("refresh did not return rotated snapshot")
			}
			client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader("RAW-SECRET-TOKEN"))}, nil
			})
			_, err = auth.Refresh(context.Background(), provider, client, current)
			if err == nil || strings.Contains(err.Error(), "RAW-SECRET") || errors.Unwrap(err) != nil {
				t.Fatal("unsafe public error")
			}
			_, err = auth.Login(context.Background(), provider, client, auth.Interaction{OpenURL: func(context.Context, string) error { return errors.New("RAW-SECRET-TOKEN") }})
			if err == nil || strings.Contains(err.Error(), "RAW-SECRET") || errors.Unwrap(err) != nil {
				t.Fatal("unsafe interaction error")
			}
		})
	}
}
