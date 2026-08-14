// Package httpheader provides the shared helper for applying user-supplied
// extra HTTP headers (provider.Call.Headers and its per-modality
// equivalents) to outgoing requests, without letting them override the
// provider's own authentication header.
package httpheader

import (
	"net/http"
	"strings"
)

// Apply sets each entry of headers on req via req.Header.Set, skipping any
// entry whose key case-insensitively matches authHeaderName — callers must
// not be able to override the provider's own authentication header via
// extra headers. An empty authHeaderName means no key is skipped. A nil (or
// empty) headers map is a no-op.
func Apply(req *http.Request, headers map[string]string, authHeaderName string) {
	ApplyToHeader(req.Header, headers, authHeaderName)
}

// ApplyToHeader is Apply's underlying implementation, operating directly on
// an http.Header rather than an *http.Request — for callers building a
// handshake header set (e.g. a WebSocket dial's DialOptions.Header) rather
// than an *http.Request. Same contract as Apply: entries are set via
// h.Set, skipping any key that case-insensitively matches authHeaderName.
func ApplyToHeader(h http.Header, headers map[string]string, authHeaderName string) {
	for k, v := range headers {
		if authHeaderName != "" && strings.EqualFold(k, authHeaderName) {
			continue
		}
		h.Set(k, v)
	}
}
