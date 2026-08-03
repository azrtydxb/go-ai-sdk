package luma

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchmedia"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// videoModel implements provider.VideoModel against Luma's asynchronous
// Dream Machine video-generations endpoint: a generation is created, then
// polled until it reaches a terminal state.
type videoModel struct {
	provider *Provider
	modelID  string
}

func (m *videoModel) ModelID() string      { return m.modelID }
func (m *videoModel) ProviderName() string { return providerName }

// ---- wire types ----

type videoCreateRequest struct {
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	Duration    string `json:"duration,omitempty"`
}

// videoGenerationResponse matches Luma's video generation status object,
// returned both by the create call and by polling
// GET .../generations/{id}.
type videoGenerationResponse struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	FailureReason string `json:"failure_reason"`
	Assets        struct {
		Video string `json:"video"`
	} `json:"assets"`
}

// durationString converts a DurationSec value into Luma's "5s"-style
// duration string, e.g. 5 -> "5s". Fractional seconds are formatted with
// the minimum number of digits needed (e.g. 2.5 -> "2.5s").
func durationString(sec float64) string {
	return strconv.FormatFloat(sec, 'f', -1, 64) + "s"
}

func (m *videoModel) GenerateVideos(ctx context.Context, call provider.VideoCall) (*provider.VideoResponse, error) {
	req := videoCreateRequest{
		Prompt:      call.Prompt,
		Model:       m.modelID,
		AspectRatio: call.AspectRatio,
		Resolution:  call.Resolution,
	}
	if call.DurationSec != 0 {
		req.Duration = durationString(call.DurationSec)
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("luma: marshal video request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("luma: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/dream-machine/v1/generations"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("luma: build video request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("luma: read video response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var created createResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, fmt.Errorf("luma: decode video response: %w", err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("luma: response contained no generation id: %s", body)
	}

	gen, rawBody, err := m.poll(ctx, created.ID)
	if err != nil {
		return nil, err
	}

	if gen.Assets.Video == "" {
		return nil, fmt.Errorf("luma: completed generation contained no video asset: %s", rawBody)
	}

	data, mediaType, err := fetchmedia.Fetch(ctx, m.provider.client(), gen.Assets.Video, "luma", 0)
	if err != nil {
		return nil, err
	}
	if mediaType == "" {
		mediaType = "video/mp4"
	}

	return &provider.VideoResponse{
		Videos: []provider.GeneratedVideo{{Data: data, MediaType: mediaType, URL: gen.Assets.Video}},
		Raw:    json.RawMessage(rawBody),
	}, nil
}

// poll repeatedly fetches the video generation status until it reaches a
// terminal state ("completed" or "failed"), sleeping p.provider.poll()
// between requests. The sleep is ctx-aware: cancellation returns
// ctx.Err() immediately instead of waiting out the interval.
func (m *videoModel) poll(ctx context.Context, id string) (*videoGenerationResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/dream-machine/v1/generations/" + id

	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("luma: build poll request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

		resp, err := m.provider.client().Do(httpReq)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("luma: read poll response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, apiError(resp, body)
		}

		var gen videoGenerationResponse
		if err := json.Unmarshal(body, &gen); err != nil {
			return nil, nil, fmt.Errorf("luma: decode poll response: %w", err)
		}

		switch gen.State {
		case "completed":
			return &gen, body, nil
		case "failed":
			if gen.FailureReason != "" {
				return nil, nil, fmt.Errorf("luma: generation %s failed: %s", id, gen.FailureReason)
			}
			return nil, nil, fmt.Errorf("luma: generation %s failed", id)
		}

		if err := sleep(ctx, m.provider.poll()); err != nil {
			return nil, nil, err
		}
	}
}
