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
	"regexp"
	"strconv"
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
	Prompt    string          `json:"prompt"`
	NumImages int             `json:"num_images,omitempty"`
	ImageSize json.RawMessage `json:"image_size,omitempty"`
	Seed      *int64          `json:"seed,omitempty"`
}

// wxhSizePattern matches the SDK's "WxH" Size convention (e.g.
// "1024x1024").
var wxhSizePattern = regexp.MustCompile(`^\d+x\d+$`)

// imageSizeValue translates ImageCall.Size into fal's image_size field.
// fal accepts either an enum name (e.g. "square_hd") or an explicit
// {"width":W,"height":H} object; when size matches the SDK's "WxH"
// convention it's translated into that object, otherwise it's passed
// through verbatim as a string (an enum name, presumably).
func imageSizeValue(size string) (json.RawMessage, error) {
	if size == "" {
		return nil, nil
	}
	if wxhSizePattern.MatchString(size) {
		w, h, _ := strings.Cut(size, "x")
		width, err := strconv.Atoi(w)
		if err != nil {
			return nil, fmt.Errorf("parse width from Size %q: %w", size, err)
		}
		height, err := strconv.Atoi(h)
		if err != nil {
			return nil, fmt.Errorf("parse height from Size %q: %w", size, err)
		}
		return json.Marshal(struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}{Width: width, Height: height})
	}
	return json.Marshal(size)
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

	imageSize, err := imageSizeValue(call.Size)
	if err != nil {
		return nil, fmt.Errorf("fal: %w", err)
	}
	req := imageRequest{
		Prompt:    call.Prompt,
		NumImages: call.N,
		ImageSize: imageSize,
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
