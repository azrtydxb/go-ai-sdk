package fetchmedia

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

func TestFetchHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4; charset=binary")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("video-bytes"))
	}))
	defer srv.Close()

	data, mediaType, err := Fetch(context.Background(), nil, srv.URL, "test", 0)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "video-bytes" {
		t.Errorf("data = %q", data)
	}
	if mediaType != "video/mp4" {
		t.Errorf("mediaType = %q, want video/mp4 (parameters stripped)", mediaType)
	}
}

func TestFetchErrorIsSinglePrefixed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL, "luma", 0)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "luma: fetch "+srv.URL+": ") {
		t.Errorf("error = %q, want single-prefixed with %q", msg, "luma: fetch "+srv.URL+": ")
	}
	if strings.Count(msg, "luma:") != 1 {
		t.Errorf("error = %q, want exactly one %q prefix (no double-wrap)", msg, "luma:")
	}
	if strings.Count(msg, "fetch ") != 1 {
		t.Errorf("error = %q, want exactly one \"fetch \" (no double-wrap)", msg)
	}

	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

func TestFetchRejectsNonHTTPScheme(t *testing.T) {
	for _, u := range []string{
		"file:///etc/passwd",
		"gopher://example.test/1/x",
		"ftp://example.test/f",
	} {
		_, _, err := Fetch(context.Background(), nil, u, "test", 0)
		if err == nil {
			t.Errorf("Fetch(%q): expected error, got nil", u)
			continue
		}
		if !strings.Contains(err.Error(), "scheme") {
			t.Errorf("Fetch(%q): error = %v, want it to mention scheme", u, err)
		}
	}
}

func TestFetchRejectsLiteralMetadataIP(t *testing.T) {
	rt := &recordingRoundTripper{}
	client := &http.Client{Transport: rt}

	_, _, err := Fetch(context.Background(), client, "http://169.254.169.254/latest/meta-data/", "test", 0)
	if err == nil {
		t.Fatal("expected error for literal 169.254.169.254")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("error = %v, want it to mention link-local", err)
	}
	if rt.calls != 0 {
		t.Errorf("RoundTrip called %d times, want 0 (must reject before making a request)", rt.calls)
	}
}

func TestFetchRejectsIPv6LinkLocal(t *testing.T) {
	rt := &recordingRoundTripper{}
	client := &http.Client{Transport: rt}

	_, _, err := Fetch(context.Background(), client, "http://[fe80::1]/", "test", 0)
	if err == nil {
		t.Fatal("expected error for literal fe80::1")
	}
	if rt.calls != 0 {
		t.Errorf("RoundTrip called %d times, want 0", rt.calls)
	}
}

func TestFetchAllowsPrivateRangeLiteralIP(t *testing.T) {
	// Generic private ranges (10/8, 192.168/16, etc.) are NOT blocked --
	// only link-local/metadata is. There's nothing listening at this
	// address in the test environment, so we just assert the failure is a
	// connection error, not a pre-request SSRF rejection. A short timeout
	// keeps the test from hanging on the dial.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _, err := Fetch(ctx, nil, "http://10.255.255.1:1/", "test", 0)
	if err == nil {
		t.Fatal("expected a connection error (nothing listening), not success")
	}
	if strings.Contains(err.Error(), "link-local") {
		t.Errorf("error = %v, private-range IP should not be rejected as link-local", err)
	}
}

func TestFetchRedirectToLinkLocalRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/secret", http.StatusFound)
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL, "test", 0)
	if err == nil {
		t.Fatal("expected error following redirect to link-local address")
	}
	if !strings.Contains(err.Error(), "link-local") {
		t.Errorf("error = %v, want it to mention link-local", err)
	}
}

func TestFetchBodyOverCapRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("0123456789")) // 10 bytes
	}))
	defer srv.Close()

	_, _, err := Fetch(context.Background(), nil, srv.URL, "test", 5)
	if err == nil {
		t.Fatal("expected error for body exceeding cap")
	}
	if !strings.Contains(err.Error(), "exceeds 5 bytes") {
		t.Errorf("error = %v, want it to mention the byte cap", err)
	}
}

func TestFetchBodyAtCapAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("01234")) // exactly 5 bytes
	}))
	defer srv.Close()

	data, _, err := Fetch(context.Background(), nil, srv.URL, "test", 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "01234" {
		t.Errorf("data = %q", data)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		base, candidate string
		want            bool
	}{
		{"https://api.bfl.ai/v1/x", "https://api.bfl.ai/v1/y", true},
		{"https://api.bfl.ai", "https://api.bfl.ai:443", false}, // Host differs (":443" is explicit)
		{"https://api.bfl.ai", "http://api.bfl.ai", false},      // scheme differs
		{"https://api.bfl.ai", "https://evil.test", false},      // host differs
		{"https://api.bfl.ai:8443/x", "https://api.bfl.ai:8443/y", true},
		{"https://api.bfl.ai", "not a url", false},
		{"not a url", "https://api.bfl.ai", false},
	}
	for _, c := range cases {
		got := SameOrigin(c.base, c.candidate)
		if got != c.want {
			t.Errorf("SameOrigin(%q, %q) = %v, want %v", c.base, c.candidate, got, c.want)
		}
	}
}

// recordingRoundTripper records how many times RoundTrip was called,
// letting a test assert that a request was never even attempted (e.g.
// because a pre-request SSRF check rejected the URL).
type recordingRoundTripper struct {
	calls int
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls++
	return nil, errors.New("recordingRoundTripper: should not be called")
}
