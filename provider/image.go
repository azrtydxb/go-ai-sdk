package provider

import (
	"context"
	"encoding/json"
)

// GeneratedImage is a single image returned by an ImageModel.
type GeneratedImage struct {
	Data      []byte // raw image bytes
	MediaType string // e.g. "image/png"
}

// ImageCall is the input to ImageModel.GenerateImages.
type ImageCall struct {
	Prompt          string
	N               int    // number of images; 0 → provider default (1)
	Size            string // e.g. "1024x1024"; "" → provider default
	AspectRatio     string // e.g. "16:9"; providers that use size ignore this and vice versa
	Seed            *int64
	ProviderOptions map[string]any
}

// ImageResponse is the outcome of an ImageModel.GenerateImages call.
type ImageResponse struct {
	Images []GeneratedImage
	Raw    json.RawMessage
}

// ImageModel generates images from a text prompt.
type ImageModel interface {
	GenerateImages(ctx context.Context, call ImageCall) (*ImageResponse, error)
	ModelID() string
	ProviderName() string
}
