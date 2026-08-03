package prodia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// imageModel implements provider.ImageModel against Prodia's v2
// synchronous /job endpoint, which returns the generated image bytes
// directly in the response body.
type imageModel struct {
	provider *Provider
	modelID  string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return providerName }

// ---- wire types ----

type jobConfig struct {
	Prompt string `json:"prompt"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Seed   *int64 `json:"seed,omitempty"`
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	cfg := jobConfig{
		Prompt: call.Prompt,
		Seed:   call.Seed,
	}
	if call.Size != "" {
		w, h, ok := strings.Cut(call.Size, "x")
		if ok {
			if width, err := strconv.Atoi(w); err == nil {
				cfg.Width = width
			}
			if height, err := strconv.Atoi(h); err == nil {
				cfg.Height = height
			}
		}
	}

	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("prodia: marshal job config: %w", err)
	}
	cfgBytes, err = applyProviderOptions(cfgBytes, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("prodia: apply provider options: %w", err)
	}

	reqBody, err := json.Marshal(struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}{Type: m.modelID, Config: cfgBytes})
	if err != nil {
		return nil, fmt.Errorf("prodia: marshal job request: %w", err)
	}

	reqURL := m.provider.baseURL + "/job"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("prodia: build image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "image/jpeg")
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prodia: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	return &provider.ImageResponse{
		Images: []provider.GeneratedImage{{Data: body, MediaType: "image/jpeg"}},
	}, nil
}
