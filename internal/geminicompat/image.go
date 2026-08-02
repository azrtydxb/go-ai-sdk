package geminicompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewImageModel returns a provider.ImageModel that speaks the Imagen
// :predict wire format against cfg, calling cfg.EndpointFor with method
// "predict".
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

type imagenInstance struct {
	Prompt string `json:"prompt"`
}

type imagenParameters struct {
	SampleCount int    `json:"sampleCount"`
	AspectRatio string `json:"aspectRatio,omitempty"`
	Seed        *int64 `json:"seed,omitempty"`
}

type imagenRequest struct {
	Instances  []imagenInstance `json:"instances"`
	Parameters imagenParameters `json:"parameters"`
}

type imagenPrediction struct {
	BytesBase64Encoded string `json:"bytesBase64Encoded"`
	MimeType           string `json:"mimeType"`
}

type imagenResponse struct {
	Predictions []imagenPrediction `json:"predictions"`
}

// pngMagic, jpegMagic, and gifMagic are the fixed magic-byte prefixes used
// to sniff decoded image data's MediaType. WebP is detected separately since
// its magic bytes aren't a fixed contiguous prefix ("RIFF" + 4-byte size +
// "WEBP").
var (
	pngMagic  = []byte("\x89PNG")
	jpegMagic = []byte("\xFF\xD8\xFF")
	gifMagic  = []byte("GIF8")
)

// sniffImageMediaType inspects decoded image bytes' magic bytes to
// determine the MediaType, for use when the server doesn't report a
// mimeType. Falls back to "image/png" when the format can't be identified.
func sniffImageMediaType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, pngMagic):
		return "image/png"
	case bytes.HasPrefix(data, jpegMagic):
		return "image/jpeg"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case bytes.HasPrefix(data, gifMagic):
		return "image/gif"
	default:
		return "image/png"
	}
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	if call.Size != "" {
		return nil, fmt.Errorf("%s: size is not supported; use AspectRatio", m.cfg.Name)
	}

	sampleCount := call.N
	if sampleCount == 0 {
		sampleCount = 1
	}

	req := imagenRequest{
		Instances: []imagenInstance{{Prompt: call.Prompt}},
		Parameters: imagenParameters{
			SampleCount: sampleCount,
			AspectRatio: call.AspectRatio,
			Seed:        call.Seed,
		},
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal image request: %w", m.cfg.Name, err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions, m.cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("%s: apply provider options: %w", m.cfg.Name, err)
	}

	url := m.cfg.EndpointFor(m.modelID, "predict")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("%s: build image request: %w", m.cfg.Name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := m.cfg.Authorize(ctx, httpReq); err != nil {
		return nil, fmt.Errorf("%s: authorize request: %w", m.cfg.Name, err)
	}

	resp, err := m.cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read image response: %w", m.cfg.Name, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr imagenResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("%s: decode image response: %w", m.cfg.Name, err)
	}

	images := make([]provider.GeneratedImage, len(wr.Predictions))
	for i, p := range wr.Predictions {
		if p.BytesBase64Encoded == "" {
			return nil, fmt.Errorf("%s: image response missing image data", m.cfg.Name)
		}
		data, err := base64.StdEncoding.DecodeString(p.BytesBase64Encoded)
		if err != nil {
			return nil, fmt.Errorf("%s: decode image bytesBase64Encoded: %w", m.cfg.Name, err)
		}
		mediaType := p.MimeType
		if mediaType == "" {
			mediaType = sniffImageMediaType(data)
		}
		images[i] = provider.GeneratedImage{Data: data, MediaType: mediaType}
	}

	return &provider.ImageResponse{
		Images: images,
		Raw:    json.RawMessage(body),
	}, nil
}
