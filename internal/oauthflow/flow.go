// Package oauthflow coordinates browser callbacks and context-aware manual input.
package oauthflow

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Result is an authorization response, not a token.
type Result struct {
	Code, State string
	Denied      bool
}

// These fixed errors contain no provider response data.
var (
	ErrDenied              = errors.New("oauth: authorization denied")
	ErrCallbackUnavailable = errors.New("oauth: callback listener unavailable; manual input required")
)

// Handler accepts only state-bound callback results. Untrusted requests cannot
// end a login, including error callbacks with missing or incorrect state.
func Handler(expectedState string, results chan<- Result) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := Result{Code: q.Get("code"), State: q.Get("state"), Denied: q.Get("error") != ""}
		if r.Method != http.MethodGet || result.State == "" || result.State != expectedState || (!result.Denied && result.Code == "") {
			http.Error(w, "invalid authorization response", http.StatusBadRequest)
			return
		}
		select {
		case results <- result:
		default:
		}
		_, _ = w.Write([]byte("Authorization response received. You may close this window."))
	}
}

// Wait races manual input against a callback and cancels and joins the losing
// prompt. Prompt implementations must return when their context is canceled.
func Wait(ctx context.Context, callbacks <-chan Result, prompt func(context.Context, string) (string, error), message, expectedState string) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		result Result
		err    error
	}
	manual := make(chan answer, 1)
	if prompt != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			input, err := prompt(ctx, message)
			manual <- answer{Parse(input), err}
		}()
		defer func() { cancel(); <-done }()
	}
	var result Result
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case result = <-callbacks:
	case a := <-manual:
		if a.err != nil {
			return Result{}, a.err
		}
		result = a.result
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if result.State == "" || result.State != expectedState {
		return Result{}, errors.New("oauth: missing code or invalid state")
	}
	if result.Denied {
		return Result{}, ErrDenied
	}
	if result.Code == "" {
		return Result{}, errors.New("oauth: missing authorization code")
	}
	return result, nil
}

// Parse accepts a full callback URL, code#state, or an encoded query.
func Parse(input string) Result {
	input = strings.TrimSpace(input)
	if u, err := url.Parse(input); err == nil && u.Host != "" {
		return Result{Code: u.Query().Get("code"), State: u.Query().Get("state"), Denied: u.Query().Get("error") != ""}
	}
	if code, state, ok := strings.Cut(input, "#"); ok {
		return Result{Code: strings.TrimSpace(code), State: strings.TrimSpace(state)}
	}
	if q, err := url.ParseQuery(input); err == nil && (q.Has("code") || q.Has("error")) {
		return Result{Code: q.Get("code"), State: q.Get("state"), Denied: q.Get("error") != ""}
	}
	return Result{Code: input}
}
