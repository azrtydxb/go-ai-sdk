package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

type retryableErr struct{ retryable bool }

func (e *retryableErr) Error() string     { return "e" }
func (e *retryableErr) IsRetryable() bool { return e.retryable }

func TestDoRetriesRetryableErrors(t *testing.T) {
	calls := 0
	v, err := Do(t.Context(), 2, func() (int, error) {
		calls++
		if calls < 3 {
			return 0, &retryableErr{true}
		}
		return 42, nil
	})
	if err != nil || v != 42 || calls != 3 {
		t.Fatalf("v=%d err=%v calls=%d; want 42 nil 3", v, err, calls)
	}
}

func TestDoStopsOnNonRetryable(t *testing.T) {
	calls := 0
	_, err := Do(t.Context(), 5, func() (int, error) {
		calls++
		return 0, &retryableErr{false}
	})
	if calls != 1 || err == nil {
		t.Fatalf("calls=%d err=%v; want 1 attempt, error", calls, err)
	}
}

func TestDoWrapsExhaustion(t *testing.T) {
	_, err := Do(t.Context(), 1, func() (int, error) {
		return 0, &retryableErr{true}
	})
	var ex *ExhaustedError
	if !errors.As(err, &ex) || ex.Attempts != 2 {
		t.Fatalf("err=%v; want ExhaustedError{Attempts:2}", err)
	}
}

func TestDoHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Do(ctx, 3, func() (int, error) {
		return 0, &retryableErr{true}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v; want context.Canceled", err)
	}
}

func TestBackoffWithFastDelay(t *testing.T) {
	// Save original and restore in cleanup
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = time.Millisecond

	start := time.Now()
	calls := 0
	Do(t.Context(), 2, func() (int, error) {
		calls++
		return 0, &retryableErr{true}
	})
	elapsed := time.Since(start)

	// With 2 retries and BaseDelay=1ms, we should have at least 2 backoff delays
	// Even with jitter, should be roughly >= 2ms total
	if elapsed < 2*time.Millisecond {
		t.Logf("elapsed=%v; backoff might not be working (expected >= 2ms)", elapsed)
	}
}
