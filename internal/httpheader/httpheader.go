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
	for k, v := range headers {
		if authHeaderName != "" && strings.EqualFold(k, authHeaderName) {
			continue
		}
		req.Header.Set(k, v)
	}
}
