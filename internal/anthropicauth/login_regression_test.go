package anthropicauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/oauthflow"
)

var callbackAddress string

func TestMain(m *testing.M) {
	listenCallback = func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			callbackAddress = ln.Addr().String()
		}
		return ln, err
	}
	os.Exit(m.Run())
}

type loginTransport func(*http.Request) (*http.Response, error)

func (f loginTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCompletePKCEIndependentState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := NewOAuthTokenSource()
	_, _ = source.CompletePKCE(ctx, &Interaction{OpenURL: func(_ context.Context, raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		hash := sha256.Sum256([]byte(q.Get("state")))
		if base64.RawURLEncoding.EncodeToString(hash[:]) == q.Get("code_challenge") {
			t.Error("authorization URL exposes the PKCE verifier as state")
		}
		cancel()
		return nil
	}})
}

func TestCompletePKCECallbackDoesNotWaitForPrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := NewOAuthTokenSource()
	source.SetHTTPClient(&http.Client{Transport: loginTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"test-access","refresh_token":"test-refresh","expires_in":3600}`)), Header: make(http.Header)}, nil
	})})
	_, err := source.CompletePKCE(ctx, &Interaction{
		OpenURL: func(ctx context.Context, raw string) error {
			u, _ := url.Parse(raw)
			if u.Query().Get("redirect_uri") != "http://localhost:53692/callback" {
				t.Error("registered redirect changed")
			}
			callback := "http://" + callbackAddress + "/callback?code=test-code&state=" + url.QueryEscape(u.Query().Get("state"))
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			return err
		},
		Prompt: func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("valid callback blocked by manual prompt: %v", err)
	}
}

func TestCompletePKCEOccupiedPortManualFallback(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	old := listenCallback
	listenCallback = func(network, _ string) (net.Listener, error) { return net.Listen(network, occupied.Addr().String()) }
	defer func() { listenCallback = old }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := NewOAuthTokenSource()
	calls := 0
	source.SetHTTPClient(&http.Client{Transport: loginTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`))}, nil
	})})
	var state string
	creds, err := source.CompletePKCE(ctx, &Interaction{
		OpenURL: func(_ context.Context, raw string) error {
			u, _ := url.Parse(raw)
			state = u.Query().Get("state")
			return nil
		},
		Prompt: func(context.Context, string) (string, error) { return "code#" + state, nil },
	})
	if err != nil || calls != 1 || creds.Access == "" {
		t.Fatalf("manual fallback failed: %v", err)
	}
	_, err = source.CompletePKCE(ctx, &Interaction{OpenURL: func(context.Context, string) error { t.Error("opened unusable callback-only login"); return nil }})
	if !errors.Is(err, oauthflow.ErrCallbackUnavailable) {
		t.Fatalf("unclear callback-only failure: %v", err)
	}
	_, err = source.CompletePKCE(ctx, &Interaction{Prompt: func(context.Context, string) (string, error) { return "code#wrong", nil }})
	if err == nil || calls != 1 {
		t.Fatal("manual fallback bypassed state validation")
	}
}

func TestCompletePKCEDenialCallbacks(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "forged-denial-then-success", true: "valid-denial"}[denied], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			source := NewOAuthTokenSource()
			calls := 0
			source.SetHTTPClient(&http.Client{Transport: loginTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"fixture-access","refresh_token":"fixture-refresh","expires_in":3600}`))}, nil
			})})
			promptDone := make(chan struct{})
			_, err := source.CompletePKCE(ctx, &Interaction{
				OpenURL: func(ctx context.Context, raw string) error {
					u, _ := url.Parse(raw)
					queries := []string{"error=access_denied&state=forged", "error=access_denied"}
					last := "code=fixture-code"
					if denied {
						last = "error=access_denied&error_description=fixture-secret"
					}
					queries = append(queries, last+"&state="+url.QueryEscape(u.Query().Get("state")))
					for i, query := range queries {
						req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+callbackAddress+"/callback?"+query, nil)
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							return err
						}
						body, _ := io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						if i < 2 && resp.StatusCode != http.StatusBadRequest {
							t.Error("accepted forged denial")
						}
						if strings.Contains(string(body), "fixture-secret") {
							t.Error("callback echoed provider data")
						}
					}
					return nil
				},
				Prompt: func(ctx context.Context, _ string) (string, error) {
					defer close(promptDone)
					<-ctx.Done()
					return "", ctx.Err()
				},
			})
			if denied {
				if !errors.Is(err, oauthflow.ErrDenied) || calls != 0 {
					t.Fatalf("denial not handled promptly: %v", err)
				}
			} else if err != nil || calls != 1 {
				t.Fatalf("forged denial terminated login: %v", err)
			}
			select {
			case <-promptDone:
			default:
				t.Error("prompt not joined")
			}
			ln, err := net.Listen("tcp", callbackAddress)
			if err != nil {
				t.Fatalf("listener leaked: %v", err)
			}
			_ = ln.Close()
		})
	}
}
