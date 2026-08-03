package fal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// videoModel implements provider.VideoModel against fal.ai's synchronous
// fal.run endpoint.
type videoModel struct {
	provider *Provider
	modelID  string
}

func (m *videoModel) ModelID() string      { return m.modelID }
func (m *videoModel) ProviderName() string { return providerName }

// ---- wire types ----

// videoRequest is deliberately minimal: fal's video models don't share a
// common field naming convention for resolution/duration/etc, so only
// Prompt and AspectRatio (which fal's video models consistently name
// "aspect_ratio") are sent as first-class fields. Anything else (including
// Resolution and DurationSec) must be passed via ProviderOptions, keyed
// under whatever field name the target model expects.
type videoRequest struct {
	Prompt      string `json:"prompt"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

type videoWire struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
}

type videoResponseWire struct {
	Video  *videoWire  `json:"video"`
	Videos []videoWire `json:"videos"`
}

// videoEntries normalizes videoResponseWire's two accepted shapes
// (a single "video" object or a "videos" array) into a single slice.
func (w videoResponseWire) videoEntries() []videoWire {
	if w.Video != nil {
		return []videoWire{*w.Video}
	}
	return w.Videos
}

func (m *videoModel) GenerateVideos(ctx context.Context, call provider.VideoCall) (*provider.VideoResponse, error) {
	req := videoRequest{
		Prompt:      call.Prompt,
		AspectRatio: call.AspectRatio,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("fal: marshal video request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("fal: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/" + m.modelID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("fal: build video request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Key "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fal: read video response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wire videoResponseWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("fal: decode video response: %w", err)
	}

	entries := wire.videoEntries()
	if len(entries) == 0 {
		return nil, fmt.Errorf("fal: response contained no videos: %s", body)
	}

	videos := make([]provider.GeneratedVideo, 0, len(entries))
	for _, v := range entries {
		data, mediaType, err := fetchVideo(ctx, m.provider.client(), v.URL)
		if err != nil {
			return nil, fmt.Errorf("fal: fetch video: %w", err)
		}
		if v.ContentType != "" {
			mediaType = v.ContentType
		}
		videos = append(videos, provider.GeneratedVideo{Data: data, MediaType: mediaType, URL: v.URL})
	}

	return &provider.VideoResponse{
		Videos: videos,
		Raw:    json.RawMessage(body),
	}, nil
}

// fetchVideo downloads the video at url using client (or http.DefaultClient
// if client is nil), returning the raw bytes and a MediaType. Unlike
// internal/fetchimage (which is image-specific: it sniffs unrecognized
// content types via internal/imagesniff), video bytes aren't sniffed —
// the MediaType is taken from the response's Content-Type header when
// present, defaulting to "video/mp4" otherwise. A non-2xx response returns
// an *ai.APICallError via apiError, so a transient CDN 5xx is retryable
// through ai core the same way a provider API error is.
func fetchVideo(ctx context.Context, client *http.Client, url string) ([]byte, string, error) {
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("fal: build fetch request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fal: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("fal: read response from %s: %w", url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", apiError(resp, body)
	}

	mediaType := "video/mp4"
	if ct := parseMediaTypeVideo(resp.Header.Get("Content-Type")); ct != "" {
		mediaType = ct
	}

	return body, mediaType, nil
}

// parseMediaTypeVideo strips any parameters (e.g. "; charset=binary") from
// a Content-Type header value, returning just the type/subtype. Returns ""
// if contentType is empty or unparseable.
func parseMediaTypeVideo(contentType string) string {
	if contentType == "" {
		return ""
	}
	if t, _, err := mime.ParseMediaType(contentType); err == nil {
		return t
	}
	t, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(t)
}
