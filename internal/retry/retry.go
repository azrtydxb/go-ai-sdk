package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// baseDelay is the base delay for exponential backoff. It is a
// package-private test-tuning knob: mutating it concurrently with an
// in-flight retry would be a data race, so production code must never write
// it. Tests that need a faster backoff call SetBaseDelayForTest instead of
// assigning to it directly.
var baseDelay = 500 * time.Millisecond

// SetBaseDelayForTest overrides the base delay used by calculateBackoff and
// returns a function that restores the previous value. It exists so tests
// (in this package and others, e.g. package ai) can speed up backoff during
// retry tests without a data race on a mutable exported global. Test-only;
// not safe for concurrent use, and callers must not mutate baseDelay while
// a retry.Do call is in flight elsewhere.
func SetBaseDelayForTest(d time.Duration) (restore func()) {
	prev := baseDelay
	baseDelay = d
	return func() { baseDelay = prev }
}

// Retryable is an interface for errors that can be checked for retryability.
type Retryable interface {
	IsRetryable() bool
}

// ExhaustedError is returned when retries are exhausted.
type ExhaustedError struct {
	Attempts int
	LastErr  error
}

// Error implements the error interface.
func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("retries exhausted after %d attempts: %v", e.Attempts, e.LastErr)
}

// Unwrap implements the error unwrapping interface.
func (e *ExhaustedError) Unwrap() error {
	return e.LastErr
}

// Do calls fn up to 1+maxRetries times (initial attempt + maxRetries retries).
// It retries only when the error implements Retryable and returns true.
// Backoff uses exponential backoff with base 500ms, doubling, full jitter, and 8s cap.
// If context is canceled, returns ctx.Err() immediately.
// After exhaustion, returns the last error unchanged if maxRetries==0,
// else wraps it in *ExhaustedError.
func Do[T any](ctx context.Context, maxRetries int, fn func() (T, error)) (T, error) {
	totalAttempts := 1 + maxRetries

	for attempt := 0; attempt < totalAttempts; attempt++ {
		// Check context before calling fn
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		default:
		}

		v, err := fn()
		if err == nil {
			return v, nil
		}

		// Check if error is retryable using errors.As to handle wrapped errors
		var retryable Retryable
		if !errors.As(err, &retryable) || !retryable.IsRetryable() {
			// Not retryable, return immediately
			return v, err
		}

		// If this was the last attempt, return wrapped error
		if attempt == totalAttempts-1 {
			if maxRetries == 0 {
				return v, err
			}
			return v, &ExhaustedError{
				Attempts: totalAttempts,
				LastErr:  err,
			}
		}

		// Calculate backoff delay
		delay := calculateBackoff(attempt)

		// Wait with context awareness, using NewTimer to ensure cleanup.
		// The timer is stopped explicitly on every path (not deferred) so
		// timers don't accumulate across loop iterations until Do returns.
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			// Continue to next attempt
		case <-ctx.Done():
			timer.Stop()
			var zero T
			return zero, ctx.Err()
		}
	}

	// Should not reach here, but return zero value just in case
	var zero T
	return zero, nil
}

// calculateBackoff calculates the backoff delay for the given attempt number.
// Uses exponential backoff (base 500ms, doubling) with full jitter and 8s cap.
func calculateBackoff(attempt int) time.Duration {
	maxDelay := 8 * time.Second
	delay := baseDelay

	// Exponentially increase delay, but stop before overflow and cap at maxDelay.
	// For each attempt, double the delay, but bail to maxDelay once we reach or exceed it.
	for i := 0; i < attempt; i++ {
		if delay >= maxDelay {
			delay = maxDelay
			break
		}
		// Double the delay, but cap to maxDelay to prevent overflow
		if delay > maxDelay/2 {
			delay = maxDelay
			break
		}
		delay *= 2
	}

	// rand.Int63n panics for n <= 0; baseDelay is a test-tuning knob that may
	// be set to 0 (or less) via SetBaseDelayForTest to speed up backoff, so
	// guard rather than panic.
	if delay <= 0 {
		return 0
	}

	// Full jitter: random value between 0 and delay
	jitter := time.Duration(rand.Int63n(delay.Nanoseconds())) * time.Nanosecond
	return jitter
}
