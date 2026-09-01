package fetchmedia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func TestSniffImageMediaType(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\nrest-of-file"), "image/png"},
		{"jpeg", []byte("\xFF\xD8\xFF\xE0rest-of-file"), "image/jpeg"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 rest-of-file"), "image/webp"},
		{"gif", []byte("GIF89arest-of-file"), "image/gif"},
		{"unknown falls back", []byte("not an image"), "image/png"},
		{"empty falls back", []byte{}, "image/png"},
		{"too short for webp check falls back", []byte("RIFF"), "image/png"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SniffImageMediaType(tc.data, "image/png"); got != tc.want {
				t.Errorf("SniffImageMediaType(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

func TestFetchImageUsesContentTypeWhenImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-really-png-but-whatever"))
	}))
	defer func() { srv.Close() }()

	data, mediaType, err := FetchImage(context.Background(), nil, srv.URL, "test")
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png (from Content-Type)", mediaType)
	}
	if string(data) != "not-really-png-but-whatever" {
		t.Errorf("data = %q", data)
	}
}

func TestFetchImageStripsContentTypeParameters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg; charset=binary")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer func() { srv.Close() }()

	_, mediaType, err := FetchImage(context.Background(), nil, srv.URL, "test")
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if mediaType != "image/jpeg" {
		t.Errorf("mediaType = %q, want image/jpeg (parameters stripped)", mediaType)
	}
}

func TestFetchImageSniffsWhenContentTypeNotImage(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nrest-of-file")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes)
	}))
	defer func() { srv.Close() }()

	data, mediaType, err := FetchImage(context.Background(), nil, srv.URL, "test")
	if err != nil {
		t.Fatalf("FetchImage: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png (sniffed)", mediaType)
	}
	if string(data) != string(pngBytes) {
		t.Errorf("data mismatch")
	}
}

func TestFetchImageNon2xxErrorIsRetryableFor5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream hiccup"))
	}))
	defer func() { srv.Close() }()

	_, _, err := FetchImage(context.Background(), nil, srv.URL, "test")
	if err == nil {
		t.Fatal("expected error for non-2xx status")
	}

	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", apiErr.StatusCode)
	}
	if !apiErr.Retryable {
		t.Error("Retryable = false, want true for a transient 5xx CDN error")
	}
}

func TestFetchImageNon2xxErrorTruncatesBody(t *testing.T) {
	bigBody := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(bigBody))
	}))
	defer func() { srv.Close() }()

	_, _, err := FetchImage(context.Background(), nil, srv.URL, "test")
	if err == nil {
		t.Fatal("expected error for non-2xx status")
	}
	// The underlying *ai.APICallError is wrapped with a
	// "test: fetch <url>: " prefix, so the bound is ~1KB (the truncated
	// body) plus that prefix and the APICallError's own boilerplate -- not
	// exactly 1024.
	if len(err.Error()) > 1300 {
		t.Errorf("error message too long (%d chars), want body truncated to ~1KB", len(err.Error()))
	}
}
