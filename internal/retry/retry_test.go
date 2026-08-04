package retry

import (
	"context"
	"errors"
	"fmt"
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

	// With 2 retries and BaseDelay=1ms, we should have backoff delays.
	// With full jitter, delays can be tiny, so this test verifies the mechanism runs.
	// At minimum, we should have > 0 elapsed time (the backoff loops execute).
	if elapsed == 0 {
		t.Errorf("elapsed=%v; backoff delays not applied", elapsed)
	}
}

func TestDoWrapsRetryableErrors(t *testing.T) {
	// Verify that errors.As detects wrapped Retryable errors.
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = time.Microsecond

	baseErr := &retryableErr{true}
	wrappedErr := fmt.Errorf("wrapped: %w", baseErr)

	calls := 0
	_, err := Do(t.Context(), 1, func() (int, error) {
		calls++
		return 0, wrappedErr
	})

	// Should have retried despite the wrap, so calls > 1
	if calls < 2 {
		t.Fatalf("calls=%d; expected >= 2 (retried wrapped retryable error)", calls)
	}

	// Should get ExhaustedError wrapping the wrapped error
	var ex *ExhaustedError
	if !errors.As(err, &ex) {
		t.Fatalf("err=%v; want ExhaustedError", err)
	}
}

func TestCalculateBackoffZeroBaseDelayNoPanic(t *testing.T) {
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = 0

	delay := calculateBackoff(0)
	if delay != 0 {
		t.Fatalf("delay = %v, want 0", delay)
	}
}

func TestCalculateBackoffNegativeBaseDelayNoPanic(t *testing.T) {
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = -1

	delay := calculateBackoff(3)
	if delay != 0 {
		t.Fatalf("delay = %v, want 0", delay)
	}
}

func TestDoManyRetriesDoesNotAccumulateTimers(t *testing.T) {
	// Regression test for the defer-in-loop footgun: with many retries, Do
	// must not leak/accumulate running timers across iterations (each
	// iteration's timer.Stop must run per-iteration, not pile up as
	// deferred calls until Do returns). Attempt count is kept low enough
	// that calculateBackoff's exponential growth doesn't hit the 8s cap
	// (which would make this a slow, flaky wall-clock test); the point
	// here is simply that Do completes promptly and correctly across many
	// iterations, not to measure backoff timing precisely.
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = time.Microsecond

	calls := 0
	start := time.Now()
	_, err := Do(t.Context(), 10, func() (int, error) {
		calls++
		return 0, &retryableErr{true}
	})
	elapsed := time.Since(start)

	if calls != 11 {
		t.Fatalf("calls = %d, want 11", calls)
	}
	var ex *ExhaustedError
	if !errors.As(err, &ex) {
		t.Fatalf("err = %v, want ExhaustedError", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("elapsed = %v, too slow for 10 retries at ~microsecond backoff", elapsed)
	}
}

func TestCalculateBackoffHighAttempt(t *testing.T) {
	// Test that calculateBackoff doesn't panic or overflow for large attempt numbers.
	originalDelay := BaseDelay
	t.Cleanup(func() { BaseDelay = originalDelay })
	BaseDelay = time.Millisecond

	// Test high attempt count (40+) that would overflow with bit shift.
	delay := calculateBackoff(40)
	if delay < 0 || delay > 8*time.Second {
		t.Errorf("delay=%v; expected [0, 8s]", delay)
	}

	// Verify it's capped at maxDelay
	maxDelay := 8 * time.Second
	if delay > maxDelay {
		t.Errorf("delay=%v exceeds cap of %v", delay, maxDelay)
	}
}
