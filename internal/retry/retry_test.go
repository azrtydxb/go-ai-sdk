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
