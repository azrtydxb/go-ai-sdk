package retry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type hintErr struct {
	d  time.Duration
	ok bool
}

func (e *hintErr) Error() string                     { return "hint" }
func (e *hintErr) IsRetryable() bool                 { return true }
func (e *hintErr) RetryAfter() (time.Duration, bool) { return e.d, e.ok }

func TestParseRetryAfter(t *testing.T) {
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	past := time.Now().Add(-30 * time.Second).UTC().Format(http.TimeFormat)
	tests := []struct {
		name     string
		in       string
		min, max time.Duration
	}{
		{"seconds", "7", 7 * time.Second, 7 * time.Second},
		{"http-date", future, 25 * time.Second, 30 * time.Second},
		{"past date", past, 0, 0},
		{"empty", "", 0, 0},
		{"garbage", "soon", 0, 0},
		{"zero", "0", 0, 0},
		{"negative", "-5", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRetryAfter(tt.in)
			if got < tt.min || got > tt.max {
				t.Fatalf("ParseRetryAfter(%q)=%v; want in [%v,%v]", tt.in, got, tt.min, tt.max)
			}
		})
	}
}

func TestDoHonoursRetryAfterOverBackoff(t *testing.T) {
	defer SetBaseDelayForTest(0)()
	calls := 0
	start := time.Now()
	_, err := Do(t.Context(), 1, func() (int, error) {
		calls++
		if calls == 1 {
			return 0, &hintErr{d: 120 * time.Millisecond, ok: true}
		}
		return 1, nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("waited only %v; want >= Retry-After", el)
	}
}

func TestDoMissingHintFallsBackToBackoff(t *testing.T) {
	defer SetBaseDelayForTest(0)()
	calls := 0
	start := time.Now()
	_, err := Do(t.Context(), 1, func() (int, error) {
		calls++
		if calls == 1 {
			return 0, &hintErr{}
		}
		return 1, nil
	})
	if err != nil || calls != 2 || time.Since(start) > 50*time.Millisecond {
		t.Fatalf("err=%v calls=%d elapsed=%v", err, calls, time.Since(start))
	}
}

func TestDoRetryAfterExceedsBudgetDoesNotSleep(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	calls := 0
	start := time.Now()
	last := &hintErr{d: time.Minute, ok: true}
	_, err := Do(ctx, 3, func() (int, error) {
		calls++
		return 0, fmt.Errorf("wrapped: %w", last)
	})
	var be *RetryAfterExceedsBudgetError
	if !errors.As(err, &be) {
		t.Fatalf("err=%v; want RetryAfterExceedsBudgetError", err)
	}
	if be.Requested != time.Minute || !errors.Is(err, last) || calls != 1 {
		t.Fatalf("requested=%v calls=%d", be.Requested, calls)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("slept despite insufficient budget: %v", time.Since(start))
	}
}
