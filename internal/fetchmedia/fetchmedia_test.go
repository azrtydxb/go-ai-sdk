package fetchmedia

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
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
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the address is blocked", err)
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
	if strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, private-range IP should not be rejected as blocked", err)
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
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the address is blocked", err)
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

// TestFetchDialTimeRebindRejected covers the DNS-rebind TOCTOU
// vulnerability: ValidateURL's pre-connect lookup and the actual dial are,
// without pinning, two independent DNS resolutions. An attacker
// controlling DNS for the target host can answer the first with a safe IP
// and the second (at dial time) with a blocked one, bypassing a
// pre-connect-only check. Fetch must reject the dial regardless, because
// PinnedTransport re-resolves and re-checks at the moment of the actual
// TCP dial.
func TestFetchDialTimeRebindRejected(t *testing.T) {
	orig := lookupIPAddr
	defer func() { lookupIPAddr = orig }()

	var calls int32
	lookupIPAddr = func(ctx context.Context, network string) ([]net.IPAddr, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// ValidateURL's pre-connect lookup: a safe, public IP.
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		// Every subsequent lookup (the dial-time re-check): rebound to the
		// cloud metadata address.
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	_, _, err := Fetch(context.Background(), nil, "http://rebind.fetchmedia.test/", "test", 0)
	if err == nil {
		t.Fatal("expected error: dial-time re-check should reject the rebound address")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the blocked address", err)
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("lookupIPAddr called %d times, want >=2 (pre-connect + dial-time)", got)
	}
}

// TestFetchHonorsRebindProtectionAcrossRedirects is the same rebind
// scenario, but the blocked IP only shows up starting on the second host
// (reached via a redirect) -- confirming dial-time pinning applies to
// every hop, not just the first request.
func TestFetchHonorsRebindProtectionAcrossRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://rebind-hop.fetchmedia.test/next", http.StatusFound)
	}))
	defer srv.Close()

	orig := lookupIPAddr
	defer func() { lookupIPAddr = orig }()
	var calls int32
	lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// CheckRedirect's ValidateURL call for the redirect target: sees
			// a safe IP.
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		// The redirect target's actual dial: rebound to a blocked address.
		return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
	}

	_, _, err := Fetch(context.Background(), nil, srv.URL, "test", 0)
	if err == nil {
		t.Fatal("expected error: redirect hop's dial-time re-check should reject the rebound address")
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Errorf("lookupIPAddr called %d times, want >=2 (redirect validation + dial-time)", got)
	}
}

// TestFetchChainsCallerCheckRedirect ensures a caller-supplied
// CheckRedirect is still consulted (and can still fail the request) after
// Fetch's own SSRF policy check passes, rather than being silently
// dropped.
func TestFetchChainsCallerCheckRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("should not be reached"))
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()

	var callerCheckRedirectCalled bool
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			callerCheckRedirectCalled = true
			return errors.New("caller policy: no redirects allowed")
		},
	}

	_, _, err := Fetch(context.Background(), client, srv.URL, "test", 0)
	if err == nil {
		t.Fatal("expected error from caller's CheckRedirect")
	}
	if !callerCheckRedirectCalled {
		t.Error("caller's CheckRedirect was never called -- it must be chained, not dropped")
	}
	if !strings.Contains(err.Error(), "no redirects allowed") {
		t.Errorf("error = %v, want it to surface the caller's CheckRedirect error", err)
	}
}

func TestIsBlockedAddrPolicy(t *testing.T) {
	cases := []struct {
		addr string
		want bool
		note string
	}{
		{"169.254.169.254", true, "IPv4 link-local metadata"},
		{"169.254.1.1", true, "IPv4 link-local"},
		{"fe80::1", true, "IPv6 link-local"},
		{"fd00:ec2::254", true, "AWS IMDSv6"},
		{"0.0.0.0", true, "IPv4 unspecified"},
		{"::", true, "IPv6 unspecified"},
		{"127.0.0.1", false, "loopback must stay allowed (httptest uses it)"},
		{"::1", false, "IPv6 loopback must stay allowed"},
		{"10.0.0.1", false, "generic private range"},
		{"192.168.1.1", false, "generic private range"},
		{"8.8.8.8", false, "public address"},
	}
	for _, c := range cases {
		addr := netip.MustParseAddr(c.addr)
		got := isBlockedAddr(addr)
		if got != c.want {
			t.Errorf("isBlockedAddr(%s) = %v, want %v (%s)", c.addr, got, c.want, c.note)
		}
	}
}

func TestSameRegistrableDomain(t *testing.T) {
	cases := []struct {
		base, candidate string
		want            bool
	}{
		{"https://api.bfl.ai", "https://api.us1.bfl.ai/poll", true},
		{"https://api.bfl.ai", "https://api.eu1.bfl.ai/poll", true},
		{"https://api.bfl.ai", "https://api.bfl.ai/v1/x", true},
		{"https://api.bfl.ai", "https://attacker.evil.tld/poll", false},
		{"https://api.bfl.ai", "http://api.bfl.ai/poll", false},     // scheme differs
		{"https://api.bfl.ai", "ftp://api.bfl.ai/poll", false},      // not http(s)
		{"https://api.bfl.ai", "https://evilbfl.ai/poll", false},    // different registrable domain
		{"https://api.bfl.ai:8443", "https://api.us1.bfl.ai", true}, // ports ignored
		{"https://api.bfl.ai", "not a url", false},
		{"not a url", "https://api.bfl.ai", false},
	}
	for _, c := range cases {
		got := SameRegistrableDomain(c.base, c.candidate)
		if got != c.want {
			t.Errorf("SameRegistrableDomain(%q, %q) = %v, want %v", c.base, c.candidate, got, c.want)
		}
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
