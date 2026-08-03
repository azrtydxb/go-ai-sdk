// Package fetchimage downloads image bytes from a URL and determines their
// MediaType, for providers (e.g. fal, replicate) that return generated
// images as URLs rather than inline data.
package fetchimage

import (
	"context"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
	"github.com/azrtydxb/go-ai-sdk/internal/imagesniff"
)

// Fetch downloads the image at url using client (or http.DefaultClient if
// client is nil), returning the raw bytes and a MediaType. The MediaType is
// taken from the response's Content-Type header (parameters such as
// "; charset=..." are stripped) when it starts with "image/"; otherwise
// it's determined by sniffing the downloaded bytes via
// imagesniff.SniffMediaType. A non-2xx response returns an *ai.APICallError
// (via ai.NewAPICallError) carrying the status code, url, and up to 1KB of
// the response body, so a transient CDN 5xx from an image host is
// retryable through ai core the same way a provider API error is.
//
// The fetch itself is guarded by internal/fetchmedia against SSRF (only
// http/https, no link-local/metadata targets, including through redirects)
// and unbounded memory use (a hard byte cap on the response body), since
// url is chosen by the remote provider's API response, not by the caller.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	body, contentType, err := fetchmedia.Fetch(ctx, client, url, "fetchimage", 0)
	if err != nil {
		return nil, "", err
	}

	var mediaType string
	if strings.HasPrefix(contentType, "image/") {
		mediaType = contentType
	} else {
		mediaType = imagesniff.SniffMediaType(body)
	}

	return body, mediaType, nil
}
