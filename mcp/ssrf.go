package mcp

import (
	"context"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
)

// ValidateURL is the public SSRF-protection helper; see WithCheckRedirect
// for the recommended usage.
func ValidateURL(ctx context.Context, rawURL string) error {
	return fetchmedia.ValidateURL(ctx, rawURL)
}

// PinnedTransport is the public SSRF-protection helper; see WithCheckRedirect
// for the recommended usage.
func PinnedTransport(base http.RoundTripper) http.RoundTripper {
	return fetchmedia.PinnedTransport(base)
}
