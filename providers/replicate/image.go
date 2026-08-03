package replicate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/fetchimage"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// imageModel implements provider.ImageModel against Replicate's synchronous
// predictions endpoint.
type imageModel struct {
	provider *Provider
	modelID  string
}

func (m *imageModel) ModelID() string      { return m.modelID }
func (m *imageModel) ProviderName() string { return providerName }

// ---- wire types ----

type imageRequest struct {
	Input imageInput `json:"input"`
}

type imageInput struct {
	Prompt      string `json:"prompt"`
	NumOutputs  int    `json:"num_outputs,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Seed        *int64 `json:"seed,omitempty"`
}

// predictionResponse matches Replicate's prediction object. Output may be a
// single URL string or an array of URL strings depending on the model, so
// it's decoded as json.RawMessage and disambiguated in outputURLs.
type predictionResponse struct {
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"`
	Error  json.RawMessage `json:"error"`
}

// outputURLs normalizes predictionResponse.Output, which Replicate may
// return as either a single URL string or an array of URL strings, into a
// slice of URLs.
func outputURLs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil, nil
		}
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, fmt.Errorf("replicate: unrecognized output shape: %s", raw)
}

// errorText extracts a human-readable message from predictionResponse.Error,
// which Replicate may report as a JSON string or an arbitrary JSON value.
func errorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func (m *imageModel) GenerateImages(ctx context.Context, call provider.ImageCall) (*provider.ImageResponse, error) {
	if call.Size != "" {
		return nil, errors.New("replicate: size is not supported; use AspectRatio")
	}

	req := imageRequest{
		Input: imageInput{
			Prompt:      call.Prompt,
			NumOutputs:  call.N,
			AspectRatio: call.AspectRatio,
			Seed:        call.Seed,
		},
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("replicate: marshal image request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("replicate: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/models/" + m.modelID + "/predictions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("replicate: build image request: %w", err)
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
		return nil, fmt.Errorf("replicate: read image response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wire predictionResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("replicate: decode image response: %w", err)
	}

	if wire.Status != "succeeded" {
		if msg := errorText(wire.Error); msg != "" {
			return nil, fmt.Errorf("replicate: prediction status %q: %s", wire.Status, msg)
		}
		return nil, fmt.Errorf("replicate: prediction status %q", wire.Status)
	}

	urls, err := outputURLs(wire.Output)
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("replicate: response contained no output images: %s", body)
	}

	images := make([]provider.GeneratedImage, 0, len(urls))
	for _, u := range urls {
		data, mediaType, err := fetchimage.Fetch(ctx, m.provider.client(), u)
		if err != nil {
			return nil, fmt.Errorf("replicate: fetch image: %w", err)
		}
		images = append(images, provider.GeneratedImage{Data: data, MediaType: mediaType})
	}

	return &provider.ImageResponse{
		Images: images,
		Raw:    json.RawMessage(body),
	}, nil
}
