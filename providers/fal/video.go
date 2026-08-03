package fal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
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
		data, mediaType, err := fetchmedia.Fetch(ctx, m.provider.client(), v.URL, "fal", 0)
		if err != nil {
			return nil, err
		}
		if mediaType == "" {
			mediaType = "video/mp4"
		}
		if v.ContentType != "" {
			// The API-declared content type takes precedence over
			// fetchVideo's HTTP Content-Type-header sniff: fal.ai reports
			// it per-video in the response body, which is more trustworthy
			// than whatever the CDN happened to send on the download.
			mediaType = v.ContentType
		}
		videos = append(videos, provider.GeneratedVideo{Data: data, MediaType: mediaType, URL: v.URL})
	}

	return &provider.VideoResponse{
		Videos: videos,
		Raw:    json.RawMessage(body),
	}, nil
}
