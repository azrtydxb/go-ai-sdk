// Package fetchmedia downloads media (image/video/audio) bytes from a
// server-chosen URL with SSRF and memory-DoS guards, for providers that
// return generated media as URLs rather than inline data.
//
// The URL being fetched is chosen by the remote provider's API response
// (e.g. a CDN link, or — in BFL's case — an absolute polling_url), not by
// the caller. Without guards, a malicious or MITM'd provider response could
// point the SDK at an internal service or the cloud metadata endpoint
// (169.254.169.254), or return an unbounded body that exhausts memory.
// Fetch defends against both.
package fetchmedia

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// MaxBytes is the default ceiling on a fetched response body, used when
// Fetch is called with maxBytes <= 0.
const MaxBytes = 256 << 20

// maxErrorBodyBytes caps how much of a non-2xx response body is included
// in the returned error.
const maxErrorBodyBytes = 1024

// maxRedirects caps the number of redirect hops Fetch will follow.
const maxRedirects = 10

// lookupIPAddr resolves host to its IP addresses. It's a package variable
// (rather than a direct net.DefaultResolver.LookupIPAddr call) purely so
// tests can substitute a fake resolver to simulate a DNS-rebind attack,
// where a hostname resolves to a safe IP on one lookup and a different,
// blocked IP on the next.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// SetLookupIPAddrForTest overrides the DNS resolver used for SSRF validation
// and returns a function that restores the previous one. It exists so tests
// in OTHER packages within this module (e.g. providers/bfl) can exercise the
// registrable-domain / SSRF gates against non-loopback hostnames without a
// live DNS lookup, keeping the suite hermetic. Test-only; not safe for
// concurrent use.
func SetLookupIPAddrForTest(fn func(ctx context.Context, host string) ([]net.IPAddr, error)) (restore func()) {
	prev := lookupIPAddr
	lookupIPAddr = fn
	return func() { lookupIPAddr = prev }
}

// Fetch GETs url using client (or http.DefaultClient if client is nil),
// returning the raw response body and a MediaType taken from the
// response's Content-Type header (parameters such as "; charset=..."
// stripped). Callers that need a smarter fallback (e.g. sniffing image
// bytes when Content-Type is missing or generic) should apply it
// themselves on top of the returned MediaType.
//
// Fetch protects against a malicious or compromised server-chosen url in
// three ways:
//
//   - SSRF (pre-connect): only http/https schemes are allowed, and the
//     target host (and every redirect hop's host) is resolved and rejected
//     if any resolved IP is blocked -- see isBlockedAddr for the exact
//     policy. At most maxRedirects (10) hops are followed, and the
//     caller's own CheckRedirect (if any) is still honored once the SSRF
//     check passes.
//   - SSRF (DNS-rebind, dial-time): a pre-connect-only check and the
//     eventual TCP dial are, by default, two independent DNS lookups -- an
//     attacker controlling DNS for the target host can answer the first
//     with a safe IP and the second with a blocked one (e.g. cloud
//     metadata), bypassing a pre-connect check entirely. When the client's
//     Transport is an *http.Transport (or nil), Fetch installs a
//     DialContext (via PinnedTransport) that re-resolves and re-checks at
//     actual dial time and dials that specific vetted IP literal, closing
//     the gap. See PinnedTransport's doc for the one case (a custom,
//     non-*http.Transport RoundTripper) where this protection isn't
//     available.
//   - Memory DoS: the response body is read with a hard cap of maxBytes
//     (MaxBytes if maxBytes <= 0); a body that would exceed the cap fails
//     with an error rather than being read into memory in full.
//
// A non-2xx response is returned as an *ai.APICallError (via
// ai.NewAPICallError), wrapped like every other failure mode.
// errPrefix is added exactly once, as fmt.Errorf("%s: fetch %s: %w",
// errPrefix, url, err) — callers must not additionally wrap the returned
// error with their own "fetch ...:" prefix.
func Fetch(ctx context.Context, client *http.Client, rawURL, errPrefix string, maxBytes int64) ([]byte, string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if maxBytes <= 0 {
		maxBytes = MaxBytes
	}

	data, mediaType, err := fetch(ctx, client, rawURL, maxBytes)
	if err != nil {
		return nil, "", fmt.Errorf("%s: fetch %s: %w", errPrefix, rawURL, err)
	}
	return data, mediaType, nil
}

func fetch(ctx context.Context, client *http.Client, rawURL string, maxBytes int64) ([]byte, string, error) {
	if err := ValidateURL(ctx, rawURL); err != nil {
		return nil, "", err
	}

	// A per-call copy of the caller's client, sharing (a dial-pinned
	// wrapper around) its Transport -- so callers' proxy/TLS/
	// connection-pool configuration is respected -- but never mutating the
	// caller's client. CheckRedirect re-validates scheme + link-local on
	// every hop, caps the hop count, and still honors the caller's own
	// CheckRedirect (if set) once the SSRF check passes.
	safeClient := &http.Client{
		Transport: PinnedTransport(client.Transport),
		Jar:       client.Jar,
		Timeout:   client.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			if err := ValidateURL(req.Context(), req.URL.String()); err != nil {
				return err
			}
			if client.CheckRedirect != nil {
				return client.CheckRedirect(req, via)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := safeClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := body
		if len(truncated) > maxErrorBodyBytes {
			truncated = truncated[:maxErrorBodyBytes]
		}
		return nil, "", ai.NewAPICallError(resp.StatusCode, rawURL, string(truncated), string(truncated))
	}

	return body, parseMediaType(resp.Header.Get("Content-Type")), nil
}

// ValidateURL rejects any URL that isn't http(s), or whose host resolves
// (directly, if it's a literal IP, or via DNS otherwise) to a blocked
// address per isBlockedAddr. Fetch calls it both before the initial
// request and, via CheckRedirect, on every subsequent redirect hop.
//
// This is a pre-connect check only: on its own it does not defend against
// DNS-rebind (see PinnedTransport for that). Callers that build their own
// guarded requests outside of Fetch (e.g. a credentialed poll to a
// server-chosen URL) can call ValidateURL directly for the same
// pre-connect protection, and should pair it with PinnedTransport for the
// same dial-time protection Fetch itself gets.
func ValidateURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q (only http and https are allowed)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("url has no host")
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if isBlockedAddr(addr) {
			return fmt.Errorf("host %s is a blocked address", host)
		}
		return nil
	}

	ips, err := lookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %s: %w", host, err)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			continue
		}
		if isBlockedAddr(addr.Unmap()) {
			return fmt.Errorf("host %s resolves to blocked address %s", host, addr)
		}
	}
	return nil
}

// imdsV6Addr is AWS's IPv6 IMDS (instance metadata service) address.
// Unlike its IPv4 counterpart (169.254.169.254, already covered by the
// link-local-unicast check below), it's a ULA (fd00::/8) address, so it
// needs an explicit check.
var imdsV6Addr = netip.MustParseAddr("fd00:ec2::254")

// isBlockedAddr reports whether addr is in the near-zero-false-positive
// SSRF blocklist:
//
//   - link-local unicast/multicast (169.254.0.0/16, fe80::/10 and their
//     multicast equivalents) -- covers the AWS/GCP/Azure IPv4 metadata
//     endpoint at 169.254.169.254.
//   - fd00:ec2::254 -- AWS's IPv6 IMDS address, which (being a ULA, not
//     link-local) the check above doesn't otherwise catch.
//   - the unspecified address (0.0.0.0 / ::) -- not a meaningful fetch
//     target, and its resolve/bind/connect behavior is platform-dependent.
//
// Deliberately NOT blocked:
//
//   - loopback (127.0.0.0/8, ::1) -- blocking it would break every
//     httptest-based fixture in this codebase (httptest binds to
//     loopback), and it isn't the SSRF target this package defends
//     against.
//   - generic private ranges (10/8, 172.16/12, 192.168/16) -- self-hosted
//     CDNs on private networks are a legitimate deployment; this remains a
//     narrow, crown-jewel-only (cloud metadata) blocklist, not a general
//     private-network firewall.
func isBlockedAddr(addr netip.Addr) bool {
	return addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified() || addr == imdsV6Addr
}

// dialContextFunc matches the signature of (*net.Dialer).DialContext and
// http.Transport.DialContext.
type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// pinnedTransportCache memoizes PinnedTransport's result keyed by the base
// RoundTripper passed in, so repeated Fetch calls against the same caller
// *http.Client (which is what supplies base, via client.Transport) reuse
// the SAME wrapped *http.Transport -- and therefore the same idle-connection
// pool -- instead of getting a fresh Clone() (and a fresh, empty pool) on
// every call. Without this, every media fetch built and immediately
// discarded its own transport, so connections were never reused across
// fetches and idle conns just lingered to IdleConnTimeout -- socket churn
// under any kind of fetch burst.
//
// The cache is process-lifetime with no eviction: entries are keyed by
// RoundTripper identity, and in practice there are only a handful of
// distinct base RoundTripper values in a process (http.DefaultClient's, and
// one per provider client an application constructs) -- not one per
// request -- so unbounded growth isn't a real concern.
//
// This is safe to share across callers: PinnedTransport's Transport only
// carries dial-time SSRF pinning (no per-call state), and everything that IS
// per-call -- CheckRedirect, the caller's Jar/Timeout -- lives on the
// *http.Client built fresh in fetch(), not on the Transport. Caching the
// Transport therefore doesn't leak state between calls.
var pinnedTransportCache sync.Map // map[any]http.RoundTripper

// pinnedTransportNilKey is the sync.Map key used when base is nil, so every
// nil (unset client.Transport) caller shares one cached pinned transport
// wrapping a clone of http.DefaultTransport, rather than each nil value
// being treated as its own cache entry (nil, being comparable, would
// actually collide fine as a map key too, but a distinct sentinel type
// keeps the intent explicit and avoids relying on that subtlety).
type pinnedTransportNilKey struct{}

// PinnedTransport returns an http.RoundTripper that defends against
// DNS-rebind SSRF: base's own DialContext (if any, or a fresh net.Dialer
// otherwise) is wrapped so that, at the moment of the actual TCP dial, the
// target host is re-resolved, every resolved IP is checked against
// isBlockedAddr, and the dial proceeds against one of the specific vetted IP
// literals -- guaranteeing every IP that gets connected to is one that was
// checked, unlike a pre-connect-only check (ValidateURL) which can be
// defeated by a resolver that answers differently between the validation
// lookup and the transport's own (later, independent) lookup.
//
// If base is an *http.Transport, the returned transport wraps base.Clone()
// with DialContext wrapped as above. If base is nil, a clone of
// http.DefaultTransport is used (matching what an *http.Client with a nil
// Transport does internally). If base is some other, custom
// http.RoundTripper, dial-time pinning isn't possible without knowledge of
// how it dials -- base is returned unchanged, and callers get only
// ValidateURL's pre-connect protection (not rebind-safe) for that
// transport. PinnedTransport never mutates base.
//
// The result is memoized per distinct base (see pinnedTransportCache): the
// first call for a given base builds and caches the wrapped transport;
// every subsequent call with an equal base returns that same instance, so
// its connection pool is reused rather than rebuilt from scratch.
func PinnedTransport(base http.RoundTripper) http.RoundTripper {
	var key any = base
	if base == nil {
		key = pinnedTransportNilKey{}
	}
	if cached, ok := pinnedTransportCache.Load(key); ok {
		return cached.(http.RoundTripper)
	}

	var t *http.Transport
	switch bt := base.(type) {
	case *http.Transport:
		t = bt.Clone()
	case nil:
		t = http.DefaultTransport.(*http.Transport).Clone()
	default:
		// Not an *http.Transport: dial-time pinning isn't possible, so
		// return (and cache) base unchanged.
		actual, _ := pinnedTransportCache.LoadOrStore(key, base)
		return actual.(http.RoundTripper)
	}

	innerDial := t.DialContext
	if innerDial == nil {
		innerDial = (&net.Dialer{}).DialContext
	}
	t.DialContext = pinnedDialContext(innerDial)

	// LoadOrStore, not Store: if two goroutines race to build the transport
	// for the same base (both missed the Load above), keep only the first
	// one built so every caller ends up sharing a single connection pool
	// rather than each racer getting -- and using -- its own.
	actual, _ := pinnedTransportCache.LoadOrStore(key, http.RoundTripper(t))
	return actual.(http.RoundTripper)
}

// pinnedDialContext wraps dial so that, for every connection it's asked to
// make, it re-resolves the target host, rejects the dial outright if any
// resolved IP is blocked, and then dials one specific vetted IP literal
// (rather than handing the original hostname to dial, which would trigger
// a second, independent, unchecked resolution).
func pinnedDialContext(dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("dial %s: %w", addr, err)
		}

		if literal, err := netip.ParseAddr(host); err == nil {
			if isBlockedAddr(literal) {
				return nil, fmt.Errorf("dial %s: %s is a blocked address", addr, literal)
			}
			return dial(ctx, network, addr)
		}

		ips, err := lookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("dial %s: resolve host %s: %w", addr, host, err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("dial %s: host %s has no resolved addresses", addr, host)
		}

		// Vet every resolved IP before dialing any of them: if ANY resolved
		// IP is blocked, reject the whole host outright rather than dialing
		// only the subset that passed -- a host that resolves to both a
		// public IP and (e.g. via a compromised or misconfigured DNS
		// record) the metadata address must not be reachable at all through
		// this path, even on its "safe" record.
		vetted := make([]netip.Addr, 0, len(ips))
		for _, ip := range ips {
			resolved, ok := netip.AddrFromSlice(ip.IP)
			if !ok {
				continue
			}
			resolved = resolved.Unmap()
			if isBlockedAddr(resolved) {
				return nil, fmt.Errorf("dial %s: host %s resolves to blocked address %s", addr, host, resolved)
			}
			vetted = append(vetted, resolved)
		}
		if len(vetted) == 0 {
			return nil, fmt.Errorf("dial %s: host %s has no usable resolved addresses", addr, host)
		}

		// Try each vetted address in resolved order, returning the first
		// successful connection. Every dial target here is one of the
		// literals just vetted above (no second, unchecked resolution — the
		// DNS-rebind defense is preserved), so failing over across records
		// is safe: a host whose first resolved address is unreachable falls
		// through to the next vetted record instead of failing outright.
		var lastErr error
		for _, v := range vetted {
			conn, err := dial(ctx, network, net.JoinHostPort(v.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, fmt.Errorf("dial %s: all %d vetted address(es) for host %s failed, last error: %w", addr, len(vetted), host, lastErr)
	}
}

// SameOrigin reports whether base and candidate share the same scheme and
// host (host includes port, per net/url.URL.Host). It's used to gate
// attaching credentials to a server-chosen redirect/polling URL: a
// mismatch means the URL points somewhere the caller didn't configure and
// shouldn't be trusted with a secret.
func SameOrigin(base, candidate string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	c, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	return b.Scheme == c.Scheme && b.Host == c.Host
}

// SameRegistrableDomain reports whether base and candidate are both
// http(s), share the same scheme, and share, heuristically, the same
// registrable domain: either an exact hostname match, or -- when hostnames
// differ -- their final two dot-separated labels are equal (e.g.
// "api.us1.bfl.ai" and "api.bfl.ai" both end in "bfl.ai"). Ports are
// ignored (unlike SameOrigin).
//
// This is a stdlib-only heuristic (no public-suffix-list dependency, e.g.
// golang.org/x/net/publicsuffix): it does NOT correctly handle multi-label
// public suffixes like "co.uk" or "github.io", where the true registrable
// domain is three labels, not two -- for those TLDs this heuristic would
// incorrectly treat e.g. "evil.co.uk" and "safe.co.uk" as sharing a
// domain. It's meant narrowly, for gating credentials to a small, known
// set of first-party provider hosts (e.g. BFL's regional API hosts, all
// under the two-label "bfl.ai"), not as a general-purpose SSRF/CSRF origin
// check. SameOrigin (exact host match) remains the right default for
// anything more general, or for hosts under a TLD where the two-label
// heuristic doesn't hold.
func SameRegistrableDomain(base, candidate string) bool {
	b, err := url.Parse(base)
	if err != nil {
		return false
	}
	c, err := url.Parse(candidate)
	if err != nil {
		return false
	}
	if b.Scheme != c.Scheme {
		return false
	}
	if b.Scheme != "http" && b.Scheme != "https" {
		return false
	}

	bHost, cHost := b.Hostname(), c.Hostname()
	if bHost == "" || cHost == "" {
		return false
	}
	if bHost == cHost {
		return true
	}

	bDomain, ok1 := lastTwoLabels(bHost)
	cDomain, ok2 := lastTwoLabels(cHost)
	return ok1 && ok2 && bDomain == cDomain
}

// lastTwoLabels returns the final two dot-separated labels of host (e.g.
// "api.us1.bfl.ai" -> "bfl.ai"), and false if host has fewer than two
// labels.
func lastTwoLabels(host string) (string, bool) {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", false
	}
	return strings.Join(labels[len(labels)-2:], "."), true
}

// parseMediaType strips any parameters (e.g. "; charset=binary") from a
// Content-Type header value, returning just the type/subtype. Returns ""
// if contentType is empty or unparseable.
func parseMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	if t, _, err := mime.ParseMediaType(contentType); err == nil {
		return t
	}
	t, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(t)
}
