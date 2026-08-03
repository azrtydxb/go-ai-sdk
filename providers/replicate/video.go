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
	"time"

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

	wire, body, err = m.resolvePrediction(ctx, wire, body)
	if err != nil {
		return nil, err
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

// resolvePrediction waits for wire (the response to the create call) to
// reach a terminal state, polling GET /v1/predictions/{id} when it comes
// back non-terminal (e.g. "starting"/"processing"): "Prefer: wait" has a
// ~60s ceiling, so a typical multi-minute video generation legitimately
// returns non-terminal rather than "succeeded" even on a correct call —
// treating that as a hard error (the old behavior) would force a caller to
// retry, creating a second (paid) prediction instead of just waiting
// longer for the first one. Polling mirrors providers/luma's discipline:
// a ctx-aware sleep between polls, terminal on succeeded/failed/canceled.
func (m *videoModel) resolvePrediction(ctx context.Context, wire predictionResponse, body []byte) (predictionResponse, []byte, error) {
	for {
		switch wire.Status {
		case "succeeded":
			return wire, body, nil
		case "failed", "canceled":
			if msg := errorText(wire.Error); msg != "" {
				return predictionResponse{}, nil, fmt.Errorf("replicate: prediction status %q: %s", wire.Status, msg)
			}
			return predictionResponse{}, nil, fmt.Errorf("replicate: prediction status %q", wire.Status)
		}

		if wire.ID == "" {
			return predictionResponse{}, nil, fmt.Errorf("replicate: non-terminal prediction status %q with no id to poll: %s", wire.Status, body)
		}

		if err := sleep(ctx, m.provider.poll()); err != nil {
			return predictionResponse{}, nil, err
		}

		var err error
		wire, body, err = m.fetchPrediction(ctx, wire.ID)
		if err != nil {
			return predictionResponse{}, nil, err
		}
	}
}

// fetchPrediction issues one GET /v1/predictions/{id} poll request.
func (m *videoModel) fetchPrediction(ctx context.Context, id string) (predictionResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/v1/predictions/" + id
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return predictionResponse{}, nil, fmt.Errorf("replicate: build poll request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return predictionResponse{}, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return predictionResponse{}, nil, fmt.Errorf("replicate: read poll response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return predictionResponse{}, nil, apiError(resp, body)
	}

	var wire predictionResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return predictionResponse{}, nil, fmt.Errorf("replicate: decode poll response: %w", err)
	}
	return wire, body, nil
}

// sleep blocks for d or until ctx is done, whichever comes first,
// returning ctx.Err() in the latter case.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
