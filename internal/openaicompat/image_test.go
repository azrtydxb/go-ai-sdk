package openaicompat

// Request-shape tests for NewImageModel, white-box (package openaicompat)
// since they exercise imageRequest/imageResponse wire types directly.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestImageRequestShape(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-image-1")

	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      2,
		Size:   "1024x1024",
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var req imageRequest
	if err := json.Unmarshal(reqs[0], &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.Model != "gpt-image-1" || req.Prompt != "a cat" || req.N != 2 || req.Size != "1024x1024" {
		t.Errorf("request = %+v, want model gpt-image-1, prompt 'a cat', n 2, size 1024x1024", req)
	}
	if req.ResponseFormat != "b64_json" {
		t.Errorf("response_format = %q, want b64_json", req.ResponseFormat)
	}

	if len(resp.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Images))
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", resp.Images[0].MediaType)
	}
	wantData, _ := base64.StdEncoding.DecodeString(compattest.OnePixelPNGBase64())
	if string(resp.Images[0].Data) != string(wantData) {
		t.Errorf("Data = %v, want decoded 1x1 PNG bytes", resp.Images[0].Data)
	}
}

func TestImageRequestShapeOmitsZeroValues(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-image-1")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	reqs := srv.Requests()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[len(reqs)-1], &raw); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if _, ok := raw["n"]; ok {
		t.Errorf("request unexpectedly contains n: %s", reqs[len(reqs)-1])
	}
	if _, ok := raw["size"]; ok {
		t.Errorf("request unexpectedly contains size: %s", reqs[len(reqs)-1])
	}
}

func TestImageAspectRatioUnsupportedErrors(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-image-1")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		AspectRatio: "16:9",
	})
	if err == nil {
		t.Fatal("GenerateImages: want error for AspectRatio, got nil")
	}
	if !strings.Contains(err.Error(), "aspect ratio is not supported; use Size") {
		t.Errorf("error = %q, want it to mention aspect ratio not supported", err.Error())
	}
}

func TestImageEmptyBaseURLErrors(t *testing.T) {
	model := NewImageModel(Config{Name: "test", APIKey: "k"}, "gpt-image-1")
	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("GenerateImages: want error for empty BaseURL, got nil")
	}
}

func TestImageAuthHeader(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL, APIKeyHeader: "api-key"}, "gpt-image-1")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if got := srv.HeaderValues("api-key"); len(got) != 1 || got[0] != "k" {
		t.Errorf("api-key header = %v, want [k]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization header = %v, want [\"\"]", got)
	}
}
