package fal

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchimage"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// imageModel implements provider.ImageModel against fal.ai's synchronous
// fal.run endpoint.
type imageModel struct {
	provider *Provider
	modelID  string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return providerName }

// ---- wire types ----

type imageRequest struct {
	Prompt    string `json:"prompt"`
	NumImages int    `json:"num_images,omitempty"`
	ImageSize string `json:"image_size,omitempty"`
	Seed      *int64 `json:"seed,omitempty"`
}

type imageResponseWire struct {
	Images []imageWire `json:"images"`
}

type imageWire struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	if call.AspectRatio != "" {
		return nil, errors.New("fal: aspect ratio is not supported; use Size")
	}

	req := imageRequest{
		Prompt:    call.Prompt,
		NumImages: call.N,
		ImageSize: call.Size,
		Seed:      call.Seed,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("fal: marshal image request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("fal: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/" + m.modelID
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("fal: build image request: %w", err)
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
		return nil, fmt.Errorf("fal: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wire imageResponseWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("fal: decode image response: %w", err)
	}
	if len(wire.Images) == 0 {
		return nil, fmt.Errorf("fal: response contained no images: %s", body)
	}

	images := make([]provider.GeneratedImage, 0, len(wire.Images))
	for _, img := range wire.Images {
		data, mediaType, err := resolveImage(ctx, m.provider, img)
		if err != nil {
			return nil, err
		}
		images = append(images, provider.GeneratedImage{Data: data, MediaType: mediaType})
	}

	return &provider.ImageResponse{
		Images: images,
		Raw:    json.RawMessage(body),
	}, nil
}

// resolveImage returns the decoded bytes and MediaType for a single image
// entry, either by decoding a "data:" URL inline or by fetching an http(s)
// URL via fetchimage.
func resolveImage(ctx context.Context, p *Provider, img imageWire) ([]byte, string, error) {
	if strings.HasPrefix(img.URL, "data:") {
		data, mediaType, err := decodeDataURL(img.URL)
		if err != nil {
			return nil, "", fmt.Errorf("fal: decode data URL: %w", err)
		}
		if img.ContentType != "" {
			mediaType = img.ContentType
		}
		return data, mediaType, nil
	}

	data, fetchedMediaType, err := fetchimage.Fetch(ctx, p.client(), img.URL)
	if err != nil {
		return nil, "", fmt.Errorf("fal: fetch image: %w", err)
	}
	mediaType := fetchedMediaType
	if img.ContentType != "" {
		mediaType = img.ContentType
	}
	return data, mediaType, nil
}

// decodeDataURL decodes a "data:<mediatype>;base64,<data>" URL.
func decodeDataURL(u string) (data []byte, mediaType string, err error) {
	rest, ok := strings.CutPrefix(u, "data:")
	if !ok {
		return nil, "", fmt.Errorf("not a data URL: %s", u)
	}
	meta, b64data, ok := strings.Cut(rest, ",")
	if !ok {
		return nil, "", fmt.Errorf("malformed data URL: missing comma")
	}
	meta = strings.TrimSuffix(meta, ";base64")
	mediaType = meta
	if mediaType == "" {
		mediaType = "image/png"
	}
	data, err = base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		return nil, "", err
	}
	return data, mediaType, nil
}
