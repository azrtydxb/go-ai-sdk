package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	listenCallback = func(network, _ string) (net.Listener, error) { return net.Listen(network, "127.0.0.1:0") }
	os.Exit(m.Run())
}

type behaviorTransport func(*http.Request) (*http.Response, error)

func (f behaviorTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func behaviorClient(calls *int) *http.Client {
	return &http.Client{Transport: behaviorTransport(func(*http.Request) (*http.Response, error) {
		*calls++
		access := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fixture"}}`)) + ".sig"
		body, _ := json.Marshal(map[string]any{"access_token": access, "refresh_token": "fixture-refresh", "expires_in": 3600})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
}

func TestCodexOccupiedPortManualFallback(t *testing.T) {
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
	var state string
	opened, prompted := false, false
	calls := 0
	client := behaviorClient(&calls)
	creds, err := Login(ctx, "codex", client, Interaction{
		OpenURL: func(_ context.Context, raw string) error {
			opened = true
			u, _ := url.Parse(raw)
			state = u.Query().Get("state")
			return nil
		},
		Prompt: func(context.Context, string) (string, error) { prompted = true; return "code#" + state, nil },
	})
	if !opened || !prompted {
		t.Fatal("occupied callback port prevented manual login interaction")
	}
	if err != nil || calls != 1 || creds.Access == "" {
		t.Fatalf("manual fallback failed: %v", err)
	}
	_, err = Login(ctx, "codex", client, Interaction{OpenURL: func(context.Context, string) error { t.Error("opened unusable callback-only login"); return nil }})
	if err == nil || err.Error() != "auth: callback listener unavailable; manual input required" {
		t.Fatalf("unclear callback-only failure: %v", err)
	}
	_, err = Login(ctx, "codex", client, Interaction{Prompt: func(context.Context, string) (string, error) { return "code#wrong", nil }})
	if err == nil || calls != 1 {
		t.Fatal("manual fallback bypassed state validation")
	}
}

func TestCodexDeniedCallbackCompletes(t *testing.T) {
	old := listenCallback
	var address string
	listenCallback = func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			address = ln.Addr().String()
		}
		return ln, err
	}
	defer func() { listenCallback = old }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Login(ctx, "codex", nil, Interaction{OpenURL: func(ctx context.Context, raw string) error {
		u, _ := url.Parse(raw)
		if u.Query().Get("redirect_uri") != "http://localhost:1455/auth/callback" {
			t.Error("registered redirect changed")
		}
		callback := "http://" + address + "/auth/callback?error=access_denied&error_description=fixture-secret&state=" + url.QueryEscape(u.Query().Get("state"))
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, callback, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}})
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("denial did not complete promptly: %v", err)
	}
	if strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("denial leaked provider data")
	}
	if err.Error() != "auth: authorization denied" {
		t.Fatalf("unclear denial: %v", err)
	}
	assertClosed(t, address)
}

func assertClosed(t *testing.T, address string) {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("callback listener leaked: %v", err)
	}
	_ = ln.Close()
}

func TestCodexIgnoresForgedDenialAndJoinsPrompt(t *testing.T) {
	old := listenCallback
	var address string
	listenCallback = func(network, _ string) (net.Listener, error) {
		ln, err := net.Listen(network, "127.0.0.1:0")
		if err == nil {
			address = ln.Addr().String()
		}
		return ln, err
	}
	defer func() { listenCallback = old }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	calls := 0
	promptDone := make(chan struct{})
	creds, err := Login(ctx, "codex", behaviorClient(&calls), Interaction{
		OpenURL: func(ctx context.Context, raw string) error {
			u, _ := url.Parse(raw)
			for _, query := range []string{"error=access_denied&state=forged", "error=access_denied", "code=fixture-code&state=" + url.QueryEscape(u.Query().Get("state"))} {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/auth/callback?"+query, nil)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return err
				}
				_ = resp.Body.Close()
				if strings.HasPrefix(query, "error=") && resp.StatusCode != http.StatusBadRequest {
					t.Error("accepted forged denial")
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
	if err != nil || calls != 1 || creds.Access == "" {
		t.Fatalf("forged denial terminated legitimate login: %v", err)
	}
	select {
	case <-promptDone:
	default:
		t.Error("losing prompt not joined")
	}
	assertClosed(t, address)
}
