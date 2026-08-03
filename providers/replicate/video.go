package replicate

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

// videoModel implements provider.VideoModel against Replicate's synchronous
// predictions endpoint.
type videoModel struct {
	provider *Provider
	modelID  string
}

func (m *videoModel) ModelID() string      { return m.modelID }
func (m *videoModel) ProviderName() string { return providerName }

// ---- wire types ----

type videoRequest struct {
	Input videoInput `json:"input"`
}

type videoInput struct {
	Prompt      string `json:"prompt"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

func (m *videoModel) GenerateVideos(ctx context.Context, call provider.VideoCall) (*provider.VideoResponse, error) {
	req := videoRequest{
		Input: videoInput{
			Prompt:      call.Prompt,
			AspectRatio: call.AspectRatio,
		},
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("replicate: marshal video request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("replicate: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/models/" + m.modelID + "/predictions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("replicate: build video request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)
	httpReq.Header.Set("Prefer", "wait")

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("replicate: read video response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wire predictionResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("replicate: decode video response: %w", err)
	}

	if wire.Status != "succeeded" {
		if msg := errorText(wire.Error); msg != "" {
			return nil, fmt.Errorf("replicate: prediction status %q: %s", wire.Status, msg)
		}
		return nil, fmt.Errorf("replicate: prediction status %q", wire.Status)
	}

	urls, err := outputURLs(wire.Output)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("replicate: response contained no output videos: %s", body)
	}

	videos := make([]provider.GeneratedVideo, 0, len(urls))
	for _, u := range urls {
		data, mediaType, err := fetchVideo(ctx, m.provider.client(), u)
		if err != nil {
			return nil, fmt.Errorf("replicate: fetch video: %w", err)
		}
		videos = append(videos, provider.GeneratedVideo{Data: data, MediaType: mediaType, URL: u})
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
		return nil, "", fmt.Errorf("replicate: build fetch request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("replicate: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("replicate: read response from %s: %w", url, err)
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
