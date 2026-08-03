package fetchimage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func TestFetchUsesContentTypeWhenImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not-really-png-but-whatever"))
	}))
	defer srv.Close()

	data, mediaType, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png (from Content-Type)", mediaType)
	}
	if string(data) != "not-really-png-but-whatever" {
		t.Errorf("data = %q", data)
	}
}

func TestFetchStripsContentTypeParameters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg; charset=binary")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fake-jpeg-bytes"))
	}))
	defer srv.Close()

	_, mediaType, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if mediaType != "image/jpeg" {
		t.Errorf("mediaType = %q, want image/jpeg (parameters stripped)", mediaType)
	}
}

func TestFetchSniffsWhenContentTypeNotImage(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nrest-of-file")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(pngBytes)
	}))
	defer srv.Close()

	data, mediaType, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if mediaType != "image/png" {
		t.Errorf("mediaType = %q, want image/png (sniffed)", mediaType)
	}
	if string(data) != string(pngBytes) {
		t.Errorf("data mismatch")
	}
}

func TestFetchNon2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found body"))
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL)
	if err == nil {
		t.Fatal("expected error for non-2xx status")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to mention status 404", err)
	}
	if !strings.Contains(err.Error(), "not found body") {
		t.Errorf("error = %v, want it to include response body", err)
	}

	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.URL != srv.URL {
		t.Errorf("URL = %q, want %q", apiErr.URL, srv.URL)
	}
	if apiErr.Retryable {
		t.Error("Retryable = true, want false for 404 (not in the retryable set)")
	}
}

func TestFetchNon2xxErrorIsRetryableFor5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream hiccup"))
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL)
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

func TestFetchNon2xxErrorTruncatesBody(t *testing.T) {
	bigBody := strings.Repeat("x", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(bigBody))
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL)
	if err == nil {
		t.Fatal("expected error for non-2xx status")
	}
	// fetchmedia wraps the underlying *ai.APICallError with an
	// "fetchimage: fetch <url>: " prefix, so the bound is ~1KB (the
	// truncated body) plus that prefix and the APICallError's own
	// boilerplate -- not exactly 1024.
	if len(err.Error()) > 1300 {
		t.Errorf("error message too long (%d chars), want body truncated to ~1KB", len(err.Error()))
	}
}

func TestFetchUsesDefaultClientWhenNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL)
	if err != nil {
		t.Fatalf("Fetch with nil client: %v", err)
	}
}

func TestFetchContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := Fetch(ctx, nil, srv.URL)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestFetchInvalidURL(t *testing.T) {
	_, _, err := Fetch(context.Background(), nil, "://not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
