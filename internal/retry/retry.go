package retry

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// BaseDelay is the base delay for exponential backoff. Can be modified for testing.
var BaseDelay = 500 * time.Millisecond

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

		// Check if error is retryable
		retryable, ok := err.(Retryable)
		if !ok || !retryable.IsRetryable() {
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

		// Wait with context awareness
		select {
		case <-time.After(delay):
			// Continue to next attempt
		case <-ctx.Done():
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
	// Calculate base delay: BaseDelay * 2^attempt
	multiplier := 1 << uint(attempt)
	baseDelay := BaseDelay * time.Duration(multiplier)

	// Cap at 8 seconds
	maxDelay := 8 * time.Second
	if baseDelay > maxDelay {
		baseDelay = maxDelay
	}

	// Full jitter: random value between 0 and baseDelay
	jitter := time.Duration(rand.Int63n(baseDelay.Nanoseconds())) * time.Nanosecond
	return jitter
}
