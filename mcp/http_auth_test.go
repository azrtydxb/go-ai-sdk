package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPTransportTokenProviderPerRequest asserts the TokenProvider is
// invoked fresh on every Send (the refresh/rotation story), and that its
// returned token is sent as "Authorization: Bearer <token>".
func TestHTTPTransportTokenProviderPerRequest(t *testing.T) {
	var gotAuths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuths = append(gotAuths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	var calls int32
	tp := TokenProviderFunc(func(ctx context.Context) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		return fmt.Sprintf("tok-%d", n), nil
	})

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithTokenProvider(tp))
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for i := 0; i < 2; i++ {
		if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
			t.Fatalf("Send[%d]: %v", i, err)
		}
		if _, err := tr.Receive(ctx); err != nil {
			t.Fatalf("Receive[%d]: %v", i, err)
		}
	}

	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("TokenProvider calls = %d, want 2", calls)
	}
	if len(gotAuths) != 2 || gotAuths[0] != "Bearer tok-1" || gotAuths[1] != "Bearer tok-2" {
		t.Fatalf("Authorization headers = %v, want [Bearer tok-1 Bearer tok-2]", gotAuths)
	}
}

// TestHTTPTransportCustomAuthHeader asserts WithAuthHeader sends the raw
// token (no Bearer prefix) under the configured header name, and does not
// also set Authorization.
func TestHTTPTransportCustomAuthHeader(t *testing.T) {
	var gotCustom, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCustom = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tp := TokenProviderFunc(func(ctx context.Context) (string, error) { return "secret-tok", nil })
	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithTokenProvider(tp), WithAuthHeader("X-Api-Key"))
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if gotCustom != "secret-tok" {
		t.Fatalf("X-Api-Key = %q, want %q", gotCustom, "secret-tok")
	}
	if gotAuth != "" {
		t.Fatalf("Authorization = %q, want empty", gotAuth)
	}
}

// TestHTTPTransportTokenProviderOverridesStaticHeader asserts a configured
// TokenProvider takes precedence over a static Authorization header.
func TestHTTPTransportTokenProviderOverridesStaticHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tp := TokenProviderFunc(func(ctx context.Context) (string, error) { return "fresh-tok", nil })
	tr := NewStreamableHTTPTransportWithOptions(srv.URL,
		withStaticHeaders(map[string]string{"Authorization": "Bearer stale-static"}),
		WithTokenProvider(tp),
	)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if gotAuth != "Bearer fresh-tok" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer fresh-tok")
	}
}

// TestHTTPTransportStaticHeadersBackwardCompat asserts
// NewStreamableHTTPTransport's behavior (static headers, no token provider,
// no retry) is unchanged by the options-based refactor.
func TestHTTPTransportStaticHeadersBackwardCompat(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, map[string]string{"Authorization": "Bearer static-tok"})
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if gotAuth != "Bearer static-tok" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer static-tok")
	}
}

// TestHTTPTransportRetryOn429ThenSuccess asserts a 429 response is retried
// after honoring its Retry-After header, and that a subsequent 200
// succeeds, with the attempt count matching expectations.
func TestHTTPTransportRetryOn429ThenSuccess(t *testing.T) {
	var attempts int32
	var firstAttemptAt, secondAttemptAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			firstAttemptAt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondAttemptAt = time.Now()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(3))
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	gap := secondAttemptAt.Sub(firstAttemptAt)
	if gap < 900*time.Millisecond {
		t.Fatalf("retry gap = %v, want >= ~1s (Retry-After honored)", gap)
	}
}

// TestHTTPTransportRetryExhausted asserts that once maxRetries attempts
// have all failed with a retryable status, Send returns an error.
func TestHTTPTransportRetryExhausted(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(2))
	setRetryBaseDelay(tr, time.Millisecond)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
	if err == nil {
		t.Fatal("Send: want error after retries exhausted, got nil")
	}
	if atomic.LoadInt32(&attempts) != 3 { // 1 initial + 2 retries
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// TestHTTPTransportNoRetryOn400 asserts a plain 4xx (non-429) response is
// never retried, even with WithHTTPRetry configured.
func TestHTTPTransportNoRetryOn400(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(3))
	setRetryBaseDelay(tr, time.Millisecond)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
	if err == nil {
		t.Fatal("Send: want error, got nil")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on 400)", attempts)
	}
}

// TestHTTPTransportRetryCtxCancelDuringBackoff asserts that a ctx
// cancellation during the backoff wait aborts the retry loop promptly
// instead of issuing another attempt.
func TestHTTPTransportRetryCtxCancelDuringBackoff(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(5))
	setRetryBaseDelay(tr, time.Hour) // backoff would otherwise far outlast the test
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`))
	if err == nil {
		t.Fatal("Send: want error from ctx cancellation, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > testTimeout {
		t.Fatalf("Send took %v, want it to abort promptly on ctx cancellation", elapsed)
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (only the initial attempt before the aborted backoff)", attempts)
	}
}

// TestHTTPTransportNoRetryMidSSEStream asserts that once an SSE response
// has been accepted (status read, drain goroutine dispatched), a
// subsequent failure is never retried: the initial Send succeeds (the
// stream is being drained asynchronously), so there is nothing to retry —
// this test pins that a malformed/erroring SSE body doesn't cause the
// transport to reissue the request.
func TestHTTPTransportNoRetryMidSSEStream(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		fl.Flush()
		// End the stream abruptly without a trailing blank line; the
		// important thing is the transport must not reissue the request.
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(3))
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}

	// Give any (incorrect) retry a moment to happen before asserting.
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (SSE stream must not be retried)", attempts)
	}
}

// TestHTTPTransportRetryOnConnectionError asserts a connection error (the
// server closing the connection outright) is retried like a 429/503.
func TestHTTPTransportRetryOnConnectionError(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("ResponseWriter does not support Hijack")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("Hijack: %v", err)
			}
			conn.Close() // simulate a connection error
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransportWithOptions(srv.URL, WithHTTPRetry(2))
	setRetryBaseDelay(tr, time.Millisecond)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// setRetryBaseDelay is a test-only hook to keep exponential-backoff-driven
// tests fast; it reaches into the unexported field directly since these
// tests live in package mcp.
func setRetryBaseDelay(tr interface{}, d time.Duration) {
	if ht, ok := tr.(*httpTransport); ok {
		ht.retryBaseDelay = d
	}
}
