package transcribeutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSleep_ReturnsNilAfterDuration(t *testing.T) {
	start := time.Now()
	if err := Sleep(context.Background(), 10*time.Millisecond); err != nil {
		t.Fatalf("Sleep: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("Sleep returned after %v, want >= 10ms", elapsed)
	}
}

func TestSleep_ReturnsCtxErrOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep = %v, want context.Canceled", err)
	}
}

func TestSleep_CancelBeforeTimerFires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := Sleep(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sleep = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Sleep took %v, want it to return promptly on cancellation", elapsed)
	}
}

func TestExtForMediaType(t *testing.T) {
	cases := []struct {
		mediaType string
		want      string
	}{
		{"audio/mpeg", ".mp3"},
		{"audio/mp3", ".mp3"},
		{"audio/wav", ".wav"},
		{"audio/x-wav", ".wav"},
		{"audio/wave", ".wav"},
		{"audio/webm", ".webm"},
		{"audio/ogg", ".ogg"},
		{"audio/flac", ".flac"},
		{"audio/x-flac", ".flac"},
		{"audio/mp4", ".m4a"},
		{"audio/x-m4a", ".m4a"},
		{"audio/m4a", ".m4a"},
		{"video/mp4", ".mp4"},
		{"", ""},
		{"application/octet-stream", ".octet-stream"},
		{"noSlash", ""},
		{"weird/", ""},
	}
	for _, tc := range cases {
		if got := ExtForMediaType(tc.mediaType); got != tc.want {
			t.Errorf("ExtForMediaType(%q) = %q, want %q", tc.mediaType, got, tc.want)
		}
	}
}
