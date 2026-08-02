// Package imagesniff sniffs the MediaType of decoded image bytes from
// their magic-byte prefix. It exists because multiple provider compat
// layers (openaicompat, geminicompat) need to determine an image's
// MediaType when the API response doesn't report one, or reports one
// unreliably.
package imagesniff

import "bytes"

// pngMagic, jpegMagic, and gifMagic are the fixed magic-byte prefixes used
// to sniff decoded image data's MediaType. WebP is detected separately since
// its magic bytes aren't a fixed contiguous prefix ("RIFF" + 4-byte size +
// "WEBP").
var (
	pngMagic  = []byte("\x89PNG")
	jpegMagic = []byte("\xFF\xD8\xFF")
	gifMagic  = []byte("GIF8")
)

// SniffMediaType inspects decoded image bytes' magic bytes to determine the
// MediaType, since some providers (e.g. xAI's grok-2-image) return JPEG
// while the API otherwise defaults to PNG, or don't report a MediaType at
// all. Falls back to "image/png" when the format can't be identified.
func SniffMediaType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		return "image/png"
	case bytes.HasPrefix(data, jpegMagic):
		return "image/jpeg"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case bytes.HasPrefix(data, gifMagic):
		return "image/gif"
	default:
		return "image/png"
	}
}
