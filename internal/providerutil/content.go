package providerutil

import (
	"mime"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TextContent concatenates the text of every TextPart in parts, in order.
// Shared by the wire converters that collapse a message's parts to a plain
// string (system prompts, text-only user messages).
func TextContent(parts []provider.ContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		if tp, ok := part.(provider.TextPart); ok {
			b.WriteString(tp.Text)
		}
	}
	return b.String()
}

// IsPDFMediaType reports whether mediaType names application/pdf, ignoring
// case and any parameters (e.g. "Application/PDF" or
// "application/pdf; name=x" both match) — mirrors how MIME type matching is
// expected to behave per RFC 2045 rather than a strict string comparison
// against the exact wire value.
func IsPDFMediaType(mediaType string) bool {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return strings.EqualFold(mediaType, "application/pdf")
	}
	return strings.EqualFold(base, "application/pdf")
}
