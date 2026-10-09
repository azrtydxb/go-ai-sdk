package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
)

func TestAPICallErrorFromResponseRetryAfter(t *testing.T) {
	future := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	tests := []struct {
		name   string
		status int
		header string
		want   bool
	}{
		{"429 seconds", 429, "3", true},
		{"503 date", 503, future, true},
		{"429 missing", 429, "", false},
		{"429 invalid", 429, "later", false},
		{"500 ignored", 500, "3", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "http://x.test/v1", nil)
			resp := &http.Response{StatusCode: tt.status, Header: http.Header{}, Request: req}
			if tt.header != "" {
				resp.Header.Set("Retry-After", tt.header)
			}
			d, ok := NewAPICallErrorFromResponse(resp, "b", "m").RetryAfter()
			if ok != tt.want || (ok && d <= 0) {
				t.Fatalf("RetryAfter()=%v,%v; want ok=%v", d, ok, tt.want)
			}
		})
	}
}

// End to end over httptest: the server's Retry-After drives the wait, and an
// unaffordable wait surfaces as *ai.RetryAfterExceedsBudgetError.
func TestRetryAfterEndToEnd(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 || r.URL.Query().Get("always") != "" {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	call := func(ctx context.Context, url string) func() (int, error) {
		return func() (int, error) {
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return 0, err
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 300 {
				return 0, NewAPICallErrorFromResponse(resp, "", "busy")
			}
			return resp.StatusCode, nil
		}
	}

	defer retry.SetBaseDelayForTest(time.Millisecond)()
	start := time.Now()
	code, err := retry.Do(t.Context(), 1, call(t.Context(), srv.URL))
	if err != nil || code != 200 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("elapsed %v; Retry-After: 1 not honoured", el)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start = time.Now()
	_, err = retry.Do(ctx, 3, call(ctx, srv.URL+"?always=1"))
	err = translateRetryErr(err)
	var be *RetryAfterExceedsBudgetError
	if !errors.As(err, &be) || be.Requested != time.Second {
		t.Fatalf("err=%v; want RetryAfterExceedsBudgetError(1s)", err)
	}
	var api *APICallError
	if !errors.As(err, &api) || api.StatusCode != 429 {
		t.Fatalf("last error not wrapped: %v", err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatalf("slept despite budget: %v", time.Since(start))
	}
}
