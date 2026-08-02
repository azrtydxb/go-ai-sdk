package openaicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/imagesniff"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewImageModel returns a provider.ImageModel that speaks the OpenAI images
// wire format against cfg.
func NewImageModel(cfg Config, modelID string) provider.ImageModel {
	return &imageModel{cfg: cfg, modelID: modelID}
}

type imageModel struct {
	cfg     Config
	modelID string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return m.cfg.Name }

// ---- wire types ----

type imageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	// Seed is intentionally not sent: OpenAI's images API has no seed
	// parameter, so provider.ImageCall.Seed is silently ignored.
}

type imageResponse struct {
	Data []imageResponseData `json:"data"`
}

type imageResponseData struct {
	B64JSON string `json:"b64_json"`
}

// imageResponseFormat picks the response_format value to send: gpt-image
// models reject the response_format field outright (HTTP 400) and always
// return b64_json, so it's omitted for those; dall-e and other models honor
// it and need it set explicitly to get b64_json back.
func imageResponseFormat(modelID string) string {
	if strings.HasPrefix(modelID, "gpt-image") {
		return ""
	}
	return "b64_json"
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}
	if call.AspectRatio != "" {
		return nil, fmt.Errorf("%s: aspect ratio is not supported; use Size", m.cfg.Name)
	}

	req := imageRequest{
		Model:  m.modelID,
		Prompt: call.Prompt,
		N:      call.N,
		Size:   call.Size,
		// gpt-image models reject response_format with a 400 and always
		// return b64_json, so it's omitted for those; dall-e and other
		// models need it set explicitly to get b64_json back.
		ResponseFormat: imageResponseFormat(m.modelID),
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal image request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions, m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: apply provider options: %w", err)
	}

	url := m.cfg.BaseURL + "/images/generations"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build image request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	m.cfg.setAuthHeader(httpReq)

	resp, err := m.cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr imageResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode image response: %w", err)
	}

	images := make([]provider.GeneratedImage, len(wr.Data))
	for i, d := range wr.Data {
		data, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("openaicompat: decode image b64_json: %w", err)
		}
		images[i] = provider.GeneratedImage{Data: data, MediaType: imagesniff.SniffMediaType(data)}
	}

	return &provider.ImageResponse{
		Images: images,
		Raw:    json.RawMessage(body),
	}, nil
}
