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

// TestPinnedTransportReusedAcrossFetches covers the connection-reuse fix:
// repeated Fetch calls against the same caller *http.Client must share one
// pinned transport (and thus its connection pool) rather than each call
// building and discarding its own via http.Transport.Clone().
func TestPinnedTransportReusedAcrossFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := &http.Client{}

	for i := 0; i < 3; i++ {
		if _, _, err := Fetch(context.Background(), client, srv.URL, "test", 0); err != nil {
			t.Fatalf("Fetch #%d: %v", i, err)
		}
	}

	first := PinnedTransport(client.Transport)
	second := PinnedTransport(client.Transport)
	if first != second {
		t.Errorf("PinnedTransport returned different instances for the same base client.Transport (%v, %v); want the cached instance reused", first, second)
	}
}

// TestPinnedTransportSharesConnectionPool observes connection reuse
// directly: with a counting/recording DialContext installed on the base
// transport, three Fetches to the same keep-alive server should dial far
// fewer than 3 times because the underlying *http.Transport (and its idle
// conn pool) is shared across calls.
func TestPinnedTransportSharesConnectionPool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	var dials int32
	dialer := &net.Dialer{}
	base := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			atomic.AddInt32(&dials, 1)
			return dialer.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{Transport: base}

	for i := 0; i < 5; i++ {
		if _, _, err := Fetch(context.Background(), client, srv.URL, "test", 0); err != nil {
			t.Fatalf("Fetch #%d: %v", i, err)
		}
	}

	if got := atomic.LoadInt32(&dials); got >= 5 {
		t.Errorf("dials = %d, want < 5 (fewer than one per fetch, via a shared/reused connection pool)", got)
	}
}

// TestPinnedTransportDistinctBasesGetDistinctInstances ensures the cache is
// keyed by base RoundTripper: two different caller clients (with different
// base Transports) must NOT share a pinned transport/connection pool.
func TestPinnedTransportDistinctBasesGetDistinctInstances(t *testing.T) {
	base1 := &http.Transport{}
	base2 := &http.Transport{}

	t1 := PinnedTransport(base1)
	t2 := PinnedTransport(base2)
	if t1 == t2 {
		t.Error("PinnedTransport returned the same instance for two distinct base transports")
	}

	// Calling again with the same bases must return the same (already
	// cached) instances as before.
	if again := PinnedTransport(base1); again != t1 {
		t.Error("PinnedTransport(base1) changed identity across calls")
	}
	if again := PinnedTransport(base2); again != t2 {
		t.Error("PinnedTransport(base2) changed identity across calls")
	}
}

// TestPinnedTransportNilBaseShared ensures every nil-Transport caller (the
// common case: an *http.Client with Transport left unset) shares a single
// cached pinned transport, wrapping http.DefaultTransport.
func TestPinnedTransportNilBaseShared(t *testing.T) {
	t1 := PinnedTransport(nil)
	t2 := PinnedTransport(nil)
	if t1 != t2 {
		t.Error("PinnedTransport(nil) returned different instances across calls; want a shared cached instance")
	}
}

// TestPinnedTransportDoesNotMutateCallerTransport guards against a caching
// bug where the returned wrapped transport is (or aliases fields of) the
// caller's own base *http.Transport: base.DialContext must remain nil (its
// original value) after PinnedTransport wraps it, and the returned pointer
// must be a distinct object.
func TestPinnedTransportDoesNotMutateCallerTransport(t *testing.T) {
	base := &http.Transport{}
	wrapped := PinnedTransport(base)

	wt, ok := wrapped.(*http.Transport)
	if !ok {
		t.Fatalf("PinnedTransport(base) = %T, want *http.Transport", wrapped)
	}
	if wt == base {
		t.Fatal("PinnedTransport returned the caller's own base Transport pointer instead of a wrapping clone")
	}
	if base.DialContext != nil {
		t.Error("PinnedTransport mutated the caller's base Transport's DialContext")
	}
	if wt.DialContext == nil {
		t.Error("wrapped transport's DialContext is nil; want the pinning wrapper installed")
	}
}

// TestPinnedDialContextFailsOverToNextVettedIP covers the multi-IP failover
// fix: when a host resolves to multiple IPs and all are vetted (none
// blocked), the dial must try them in resolved order and succeed on the
// first one that connects, rather than giving up after the first (dead)
// address.
func TestPinnedDialContextFailsOverToNextVettedIP(t *testing.T) {
	restore := SetLookupIPAddrForTest(func(ctx context.Context, host string) ([]net.IPAddr, error) {
		// Two public, non-blocked IPs. Neither is dialed for real -- the
		// fake dial below intercepts both -- so these don't need to be
		// reachable.
		return []net.IPAddr{
			{IP: net.ParseIP("203.0.113.1")}, // dead: fake dial refuses this one
			{IP: net.ParseIP("203.0.113.2")}, // good: fake dial accepts this one
		}, nil
	})
	defer restore()

	var dialedAddrs []string
	fakeDial := dialContextFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialedAddrs = append(dialedAddrs, addr)
		host, _, _ := net.SplitHostPort(addr)
		if host == "203.0.113.1" {
			return nil, errors.New("connection refused")
		}
		clientConn, serverConn := net.Pipe()
		serverConn.Close()
		return clientConn, nil
	})

	conn, err := pinnedDialContext(fakeDial)(context.Background(), "tcp", "failover.fetchmedia.test:443")
	if err != nil {
		t.Fatalf("pinnedDialContext: %v, want success via failover to the second vetted IP", err)
	}
	conn.Close()

	want := []string{"203.0.113.1:443", "203.0.113.2:443"}
	if len(dialedAddrs) != len(want) {
		t.Fatalf("dialedAddrs = %v, want %v", dialedAddrs, want)
	}
	for i := range want {
		if dialedAddrs[i] != want[i] {
			t.Errorf("dialedAddrs[%d] = %q, want %q", i, dialedAddrs[i], want[i])
		}
	}
}

// TestPinnedDialContextAllVettedIPsFail ensures a host whose every resolved
// (vetted) IP fails to dial surfaces an error, rather than looping forever
// or silently succeeding.
func TestPinnedDialContextAllVettedIPsFail(t *testing.T) {
	restore := SetLookupIPAddrForTest(func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("203.0.113.1")},
			{IP: net.ParseIP("203.0.113.2")},
		}, nil
	})
	defer restore()

	fakeDial := dialContextFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})

	_, err := pinnedDialContext(fakeDial)(context.Background(), "tcp", "alldead.fetchmedia.test:443")
	if err == nil {
		t.Fatal("expected error when every vetted IP fails to dial")
	}
}

// TestPinnedDialContextRejectsHostWithAnyBlockedIP is the "unchanged"
// regression case for the failover fix: a host that resolves to a mix of a
// public IP and a blocked IP must still be rejected outright -- the good
// record must NOT be dialed as a fallback -- matching the pre-failover
// "reject if any resolved IP is blocked" policy.
func TestPinnedDialContextRejectsHostWithAnyBlockedIP(t *testing.T) {
	restore := SetLookupIPAddrForTest(func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{
			{IP: net.ParseIP("203.0.113.1")},     // public, fine on its own
			{IP: net.ParseIP("169.254.169.254")}, // blocked
		}, nil
	})
	defer restore()

	var dialed bool
	fakeDial := dialContextFunc(func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("should not be called")
	})

	_, err := pinnedDialContext(fakeDial)(context.Background(), "tcp", "mixed.fetchmedia.test:443")
	if err == nil {
		t.Fatal("expected error: host resolves to a blocked address among others")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the blocked address", err)
	}
	if dialed {
		t.Error("dial was called despite one resolved IP being blocked; must reject the whole host pre-dial")
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

// funcRoundTripper is an http.RoundTripper implemented as a bare func value
// (no pointer receiver, no wrapping struct) -- the shape that's unhashable
// and therefore panics if ever used directly as a sync.Map/map key. Real
// callers plausibly construct a custom RoundTripper this way (e.g.
// `http.RoundTripper(roundTripFunc(fn))`), so PinnedTransport must not
// touch pinnedTransportCache with a base of this shape.
type funcRoundTripper func(req *http.Request) (*http.Response, error)

func (f funcRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestPinnedTransportFuncRoundTripperDoesNotPanic pins the fix for a
// regression: PinnedTransport used to key pinnedTransportCache directly on
// base (`var key any = base`) before checking whether base is even an
// *http.Transport. A func-typed RoundTripper is unhashable, so
// sync.Map.Load/LoadOrStore on that key panicked with "hash of unhashable
// type" -- reachable from the public API via any provider's
// WithHTTPClient(client) whose client.Transport is a func-typed
// RoundTripper (or any other unhashable custom RoundTripper), on every
// media/result-URL fetch. PinnedTransport must type-switch on base BEFORE
// touching the cache: only *http.Transport (and nil) go through the
// pinning+cache path; any other RoundTripper must be handled without ever
// being used as a map key.
func TestPinnedTransportFuncRoundTripperDoesNotPanic(t *testing.T) {
	var calls int
	base := funcRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("funcRoundTripper: should not be called")
	})

	var got http.RoundTripper
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PinnedTransport panicked with a func-typed (unhashable) base RoundTripper: %v", r)
			}
		}()
		got = PinnedTransport(base)
	}()

	if got == nil {
		t.Fatal("PinnedTransport(funcRoundTripper) returned nil")
	}
	// Calling it again must also not panic (exercises the cache-hit path,
	// if any, for this base).
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("second PinnedTransport call panicked: %v", r)
			}
		}()
		PinnedTransport(base)
	}()
}

// TestFetchWithFuncRoundTripperClientDoesNotPanic exercises the same
// regression through the public Fetch entry point, matching how a real
// caller would hit it: a *http.Client whose Transport is a func-typed
// RoundTripper, passed to a provider via WithHTTPClient, used for a media
// fetch. Fetch must not panic, and must still get a response back via the
// (unpinned-fallback) transport.
func TestFetchWithFuncRoundTripperClientDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	base := funcRoundTripper(func(req *http.Request) (*http.Response, error) {
		return http.DefaultTransport.RoundTrip(req)
	})
	client := &http.Client{Transport: base}

	var data []byte
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Fetch panicked with a func-typed (unhashable) client.Transport: %v", r)
			}
		}()
		data, _, err = Fetch(context.Background(), client, srv.URL, "test", 0)
	}()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("data = %q, want %q", data, "data")
	}
}
