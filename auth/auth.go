// Package auth provides OAuth login (browser and device-code), refresh, and
// an owner-only credential file store for subscription providers. Login and
// Refresh never touch disk; Save, Load, and Source do, and only at the path
// the caller names. Callers must not log credentials.
package auth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/anthropicauth"
	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/internal/oauthflow"
)

// Credentials is an in-memory OAuth credential snapshot for caller persistence.
type Credentials struct {
	Access    string
	Refresh   string
	Expires   time.Time
	AccountID string
}

// Interaction supplies browser and optional manual-input capabilities. Both
// functions must respect cancellation. Prompt must accept full callback URLs
// or code#state, and return promptly when its context is canceled, including
// when a browser callback wins. Nil Prompt selects callback-only login.
type Interaction struct {
	OpenURL func(context.Context, string) error
	Prompt  func(context.Context, string) (string, error)
}

// Login completes browser OAuth for "codex" or "anthropic" within five minutes
// (or the caller's shorter deadline). A nil client uses http.DefaultClient.
// If the callback listener is unavailable, a non-nil Prompt permits manual
// login; callback-only login fails immediately. State-validated provider
// denials terminate login with a sanitized error.
// It never writes credential files or accesses a keyring.
func Login(ctx context.Context, provider string, client *http.Client, interaction Interaction) (Credentials, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	if interaction.OpenURL == nil && interaction.Prompt == nil {
		return Credentials{}, errors.New("auth: browser or prompt interaction required")
	}
	switch provider {
	case "anthropic":
		source := anthropicauth.NewOAuthTokenSource()
		source.SetHTTPClient(client)
		c, err := source.CompletePKCE(ctx, &anthropicauth.Interaction{OpenURL: interaction.OpenURL, Prompt: interaction.Prompt})
		if err != nil {
			return Credentials{}, safeError(ctx, "login", err)
		}
		return Credentials{Access: c.Access, Refresh: c.Refresh, Expires: c.Expires}, nil
	case "codex":
		c, err := loginCodex(ctx, client, interaction)
		if err != nil {
			return Credentials{}, safeError(ctx, "login", err)
		}
		return Credentials(c), nil
	default:
		return Credentials{}, errors.New("auth: unsupported provider")
	}
}

// Refresh forces a provider-specific refresh and returns the updated snapshot.
// The caller must persist rotated refresh tokens. A nil client uses the default.
func Refresh(ctx context.Context, provider string, client *http.Client, current Credentials) (Credentials, error) {
	if err := ctx.Err(); err != nil {
		return Credentials{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	if current.Refresh == "" {
		return Credentials{}, errors.New("auth: refresh token required")
	}
	switch provider {
	case "codex":
		c, err := codexauth.RefreshToken(ctx, client, current.Refresh)
		if err != nil {
			return Credentials{}, safeError(ctx, "refresh", err)
		}
		return Credentials(c), nil
	case "anthropic":
		source := anthropicauth.NewOAuthTokenSourceWithCredentials(anthropicauth.Credentials{Access: current.Access, Refresh: current.Refresh, Expires: current.Expires})
		source.SetHTTPClient(client)
		if err := source.Refresh(ctx); err != nil {
			return Credentials{}, safeError(ctx, "refresh", err)
		}
		c := source.Credentials()
		return Credentials{Access: c.Access, Refresh: c.Refresh, Expires: c.Expires, AccountID: current.AccountID}, nil
	default:
		return Credentials{}, errors.New("auth: unsupported provider")
	}
}

// Do not wrap underlying errors: token endpoints and interaction callbacks can
// include secrets in their errors, including errors reachable through Unwrap.
func safeError(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, oauthflow.ErrDenied) {
		return errors.New("auth: authorization denied")
	}
	if errors.Is(err, oauthflow.ErrCallbackUnavailable) {
		return errors.New("auth: callback listener unavailable; manual input required")
	}
	return errors.New("auth: " + operation + " failed")
}

var listenCallback = net.Listen

func loginCodex(ctx context.Context, client *http.Client, inter Interaction) (codexauth.Credential, error) {
	login := codexauth.StartBrowserLogin()
	callback, err := url.Parse(login.CallbackURL)
	if err != nil {
		return codexauth.Credential{}, err
	}
	listener, err := listenCallback("tcp", net.JoinHostPort("127.0.0.1", callback.Port()))
	if err != nil && inter.Prompt == nil {
		return codexauth.Credential{}, oauthflow.ErrCallbackUnavailable
	}
	var results chan oauthflow.Result
	if err == nil {
		results = make(chan oauthflow.Result, 1)
		mux := http.NewServeMux()
		mux.HandleFunc(callback.Path, oauthflow.Handler(login.State, results))
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		done := make(chan struct{})
		go func() { defer close(done); _ = server.Serve(listener) }()
		defer func() { _ = server.Close(); _ = listener.Close(); <-done }()
	}
	if inter.OpenURL != nil {
		if err := inter.OpenURL(ctx, login.LoginURL); err != nil {
			return codexauth.Credential{}, err
		}
	}
	result, err := oauthflow.Wait(ctx, results, inter.Prompt, "Paste the full callback URL or code#state:\n"+login.LoginURL, login.State)
	if err != nil {
		return codexauth.Credential{}, err
	}
	return codexauth.ExchangeAuthorizationCode(ctx, client, result.Code, result.State, login.CodeVerifier)
}
