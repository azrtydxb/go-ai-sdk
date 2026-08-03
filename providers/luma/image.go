package luma

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchimage"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// imageModel implements provider.ImageModel against Luma's asynchronous
// Dream Machine generations endpoint: a generation is created, then polled
// until it reaches a terminal state.
type imageModel struct {
	provider *Provider
	modelID  string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return providerName }

// ---- wire types ----

type createRequest struct {
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}

type createResponse struct {
	ID string `json:"id"`
}

// generationResponse matches Luma's generation status object, returned both
// by the create call and by polling GET .../generations/{id}.
type generationResponse struct {
	ID            string `json:"id"`
	State         string `json:"state"`
	FailureReason string `json:"failure_reason"`
	Assets        struct {
		Image string `json:"image"`
	} `json:"assets"`
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	if call.Size != "" {
		return nil, errors.New("luma: size is not supported; use AspectRatio")
	}
	if call.N > 1 {
		return nil, errors.New("luma: multiple images per call are not supported")
	}
	// Seed is not supported by Luma's Dream Machine API and is silently
	// ignored.

	req := createRequest{
		Prompt:      call.Prompt,
		Model:       m.modelID,
		AspectRatio: call.AspectRatio,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("luma: marshal image request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("luma: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/dream-machine/v1/generations/image"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("luma: build image request: %w", err)
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
		return nil, fmt.Errorf("luma: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var created createResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return nil, fmt.Errorf("luma: decode image response: %w", err)
	}
	if created.ID == "" {
		return nil, fmt.Errorf("luma: response contained no generation id: %s", body)
	}

	gen, rawBody, err := m.poll(ctx, created.ID)
	if err != nil {
		return nil, err
	}

	if gen.Assets.Image == "" {
		return nil, fmt.Errorf("luma: completed generation contained no image asset: %s", rawBody)
	}

	data, mediaType, err := fetchimage.Fetch(ctx, m.provider.client(), gen.Assets.Image, "luma")
	if err != nil {
		return nil, err
	}

	return &provider.ImageResponse{
		Images: []provider.GeneratedImage{{Data: data, MediaType: mediaType}},
		Raw:    json.RawMessage(rawBody),
	}, nil
}

// poll repeatedly fetches the generation status until it reaches a
// terminal state ("completed" or "failed"), sleeping p.provider.poll()
// between requests. The sleep is ctx-aware: cancellation returns
// ctx.Err() immediately instead of waiting out the interval.
func (m *imageModel) poll(ctx context.Context, id string) (*generationResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/dream-machine/v1/generations/" + id

	// Poll immediately on entry (a generation may already be complete by
	// the time we ask), then sleep p.provider.poll() between each
	// subsequent attempt — the sleep only runs after a non-terminal
	// response, never before the first poll.
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

		var gen generationResponse
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
