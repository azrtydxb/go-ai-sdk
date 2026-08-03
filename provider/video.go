package provider

import (
	"context"
	"encoding/json"
)

// GeneratedVideo is a single video returned by a VideoModel.
type GeneratedVideo struct {
	Data      []byte // the video bytes (providers download from result URLs)
	MediaType string // e.g. "video/mp4"
	URL       string // source URL when the provider returned one (may expire)
}

// VideoCall is the input to VideoModel.GenerateVideos.
type VideoCall struct {
	Prompt          string
	AspectRatio     string  // e.g. "16:9"; empty = provider default
	Resolution      string  // e.g. "720p"; empty = provider default
	DurationSec     float64 // 0 = provider default
	ProviderOptions map[string]any
}

// VideoResponse is the outcome of a VideoModel.GenerateVideos call.
type VideoResponse struct {
	Videos []GeneratedVideo
	Raw    json.RawMessage
}

// VideoModel generates videos from a text prompt.
type VideoModel interface {
	GenerateVideos(ctx context.Context, call VideoCall) (*VideoResponse, error)
	ModelID() string
	ProviderName() string
}
