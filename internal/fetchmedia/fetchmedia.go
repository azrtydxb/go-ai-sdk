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

// Fetch GETs url using client (or http.DefaultClient if client is nil),
// returning the raw response body and a MediaType taken from the
// response's Content-Type header (parameters such as "; charset=..."
// stripped). Callers that need a smarter fallback (e.g. sniffing image
// bytes when Content-Type is missing or generic) should apply it
// themselves on top of the returned MediaType.
//
// Fetch protects against a malicious or compromised server-chosen url in
// two ways:
//
//   - SSRF: only http/https schemes are allowed, and the target host (and
//     every redirect hop's host) is resolved and rejected if any resolved
//     IP is link-local or a link-local-range metadata address
//     (169.254.0.0/16, fe80::/10). This is a narrow, near-zero-false-positive
//     default: generic private ranges (10/8, 192.168/16, etc.) are NOT
//     blocked, since self-hosted CDNs on private networks are legitimate.
//     At most maxRedirects (10) hops are followed.
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

	// A per-call copy of the caller's client, sharing its Transport (so
	// callers' proxy/TLS/connection-pool configuration is respected) but
	// never mutating it: CheckRedirect re-validates scheme + link-local on
	// every hop, and caps the hop count.
	safeClient := &http.Client{
		Transport: client.Transport,
		Jar:       client.Jar,
		Timeout:   client.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return ValidateURL(req.Context(), req.URL.String())
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
// (directly, if it's a literal IP, or via DNS otherwise) to a link-local or
// metadata address. It's called both before the initial request and, via
// CheckRedirect, on every subsequent redirect hop.
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
			return fmt.Errorf("host %s is a link-local/metadata address", host)
		}
		return nil
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host %s: %w", host, err)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			continue
		}
		if isBlockedAddr(addr.Unmap()) {
			return fmt.Errorf("host %s resolves to link-local/metadata address %s", host, addr)
		}
	}
	return nil
}

// isBlockedAddr reports whether addr is in the near-zero-false-positive
// SSRF blocklist: link-local unicast (169.254.0.0/16, fe80::/10) and
// link-local multicast. Generic private ranges (10/8, 172.16/12,
// 192.168/16) are deliberately NOT blocked — self-hosted CDNs on private
// networks are a legitimate deployment, and the crown-jewel target (cloud
// metadata services at 169.254.169.254) is fully covered by the
// link-local check alone.
func isBlockedAddr(addr netip.Addr) bool {
	return addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast()
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
