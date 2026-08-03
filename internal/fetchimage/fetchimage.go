// Package fetchimage downloads image bytes from a URL and determines their
// MediaType, for providers (e.g. fal, replicate) that return generated
// images as URLs rather than inline data.
package fetchimage

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/imagesniff"
)

// maxErrorBodyBytes caps how much of a non-2xx response body is included
// in the returned error.
const maxErrorBodyBytes = 1024

// Fetch downloads the image at url using client (or http.DefaultClient if
// client is nil), returning the raw bytes and a MediaType. The MediaType is
// taken from the response's Content-Type header (parameters such as
// "; charset=..." are stripped) when it starts with "image/"; otherwise
// it's determined by sniffing the downloaded bytes via
// imagesniff.SniffMediaType. A non-2xx response returns an error including
// the status code and up to 1KB of the response body.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("fetchimage: build request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetchimage: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("fetchimage: read response from %s: %w", url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		truncated := body
		if len(truncated) > maxErrorBodyBytes {
			truncated = truncated[:maxErrorBodyBytes]
		}
		return nil, "", fmt.Errorf("fetchimage: fetch %s: status %d: %s", url, resp.StatusCode, truncated)
	}

	contentType := parseMediaType(resp.Header.Get("Content-Type"))
	var mediaType string
	if strings.HasPrefix(contentType, "image/") {
		mediaType = contentType
	} else {
		mediaType = imagesniff.SniffMediaType(body)
	}

	return body, mediaType, nil
}

// parseMediaType strips any parameters (e.g. "; charset=binary") from a
// Content-Type header value, returning just the type/subtype so
// GeneratedImage.MediaType stays a bare MediaType per its contract. Falls
// back to cutting on the first ';' if mime.ParseMediaType can't parse the
// header (e.g. a malformed or empty value).
func parseMediaType(contentType string) string {
	if t, _, err := mime.ParseMediaType(contentType); err == nil {
		return t
	}
	t, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(t)
}
