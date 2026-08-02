package openaicompat

// Request-shape tests for NewImageModel, white-box (package openaicompat)
// since they exercise imageRequest/imageResponse wire types directly.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	// gpt-image models reject response_format with a 400, so it must be
	// entirely absent from the request body, not merely empty.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0], &raw); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if _, ok := raw["response_format"]; ok {
		t.Errorf("request unexpectedly contains response_format for gpt-image model: %s", reqs[0])
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

func TestImageResponseFormatSentForNonGPTImageModels(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "dall-e-3")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	reqs := srv.Requests()
	var req imageRequest
	if err := json.Unmarshal(reqs[len(reqs)-1], &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.ResponseFormat != "b64_json" {
		t.Errorf("response_format = %q, want b64_json for dall-e-3", req.ResponseFormat)
	}
}

func TestImageMediaTypeSniffsJPEG(t *testing.T) {
	// A minimal JPEG magic-byte prefix followed by arbitrary bytes; the
	// sniffer only inspects the leading bytes, so this doesn't need to be a
	// valid decodable JPEG.
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	b64 := base64.StdEncoding.EncodeToString(jpegBytes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, b64)
	}))
	defer srv.Close()

	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "grok-2-image")

	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Images))
	}
	if resp.Images[0].MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", resp.Images[0].MediaType)
	}
}
