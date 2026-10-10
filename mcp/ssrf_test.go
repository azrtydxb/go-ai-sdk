package mcp

import (
	"context"
	"net/http"
	"testing"
)

// TestValidateURLRejectsBlockedURL verifies that the public ValidateURL
// wrapper rejects the addresses on fetchmedia's SSRF blocklist — a
// deliberately narrow, cloud-metadata-only list (see isBlockedAddr):
// link-local (169.254.0.0/16, fe80::/10), the AWS IPv6 IMDS address, and
// the unspecified address. Loopback and generic private ranges are
// deliberately NOT blocked.
func TestValidateURLRejectsBlockedURL(t *testing.T) {
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data",
		"http://[fd00:ec2::254]/latest/meta-data",
		"http://0.0.0.0/messages",
		"http://[fe80::1]/messages",
	} {
		if err := ValidateURL(context.Background(), raw); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", raw)
		}
	}
}

// TestValidateURLAllowsLoopback locks in the documented blocklist scope:
// loopback stays allowed so httptest-style fixtures against a local MCP
// server keep working.
func TestValidateURLAllowsLoopback(t *testing.T) {
	if err := ValidateURL(context.Background(), "http://127.0.0.1:8080/messages"); err != nil {
		t.Errorf("ValidateURL(loopback) = %v, want nil", err)
	}
}

// TestPinnedTransportNonNil verifies the public PinnedTransport wrapper
// returns a usable transport for the common inputs.
func TestPinnedTransportNonNil(t *testing.T) {
	if got := PinnedTransport(http.DefaultTransport); got == nil {
		t.Error("PinnedTransport(http.DefaultTransport) = nil, want non-nil")
	}
	if got := PinnedTransport(nil); got == nil {
		t.Error("PinnedTransport(nil) = nil, want non-nil")
	}
	if got := PinnedTransport(http.DefaultTransport); got != PinnedTransport(http.DefaultTransport) {
		t.Error("PinnedTransport memoizes per base: equal bases must yield the same instance")
	}
}
