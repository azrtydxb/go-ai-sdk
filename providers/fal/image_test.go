package fal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateImages_RequestShape(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("d")) + `","content_type":"image/png"}]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	seed := int64(42)
	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      2,
		Size:   "square_hd",
		Seed:   &seed,
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotPath != "/fal-ai/flux/schnell" {
		t.Errorf("path = %q, want /fal-ai/flux/schnell", gotPath)
	}
	if gotAuth != "Key test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Key test-key")
	}
	if gotBody["prompt"] != "a cat" {
		t.Errorf("prompt = %v", gotBody["prompt"])
	}
	if gotBody["num_images"] != float64(2) {
		t.Errorf("num_images = %v, want 2", gotBody["num_images"])
	}
	if gotBody["image_size"] != "square_hd" {
		t.Errorf("image_size = %v", gotBody["image_size"])
	}
	if gotBody["seed"] != float64(42) {
		t.Errorf("seed = %v, want 42", gotBody["seed"])
	}
}

func TestGenerateImages_OmitsZeroValues(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("pngdata")) + `"}]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if _, ok := gotBody["num_images"]; ok {
		t.Errorf("num_images should be omitted when 0, got %v", gotBody["num_images"])
	}
	if _, ok := gotBody["image_size"]; ok {
		t.Errorf("image_size should be omitted when empty, got %v", gotBody["image_size"])
	}
	if _, ok := gotBody["seed"]; ok {
		t.Errorf("seed should be omitted when nil, got %v", gotBody["seed"])
	}
}

func TestGenerateImages_AspectRatioUnsupported(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.ImageModel("fal-ai/flux/schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		AspectRatio: "16:9",
	})
	if err == nil {
		t.Fatal("expected error for AspectRatio")
	}
	if err.Error() != "fal: aspect ratio is not supported; use Size" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGenerateImages_ProviderOptionsMergeTopLevel(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[{"url":"data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte("d")) + `"}]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"fal": map[string]any{
				"prompt":              "overridden prompt",
				"guidance_scale":      7.5,
				"num_inference_steps": 4,
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotBody["prompt"] != "overridden prompt" {
		t.Errorf("prompt = %v, want override", gotBody["prompt"])
	}
	if gotBody["guidance_scale"] != 7.5 {
		t.Errorf("guidance_scale = %v, want passthrough 7.5", gotBody["guidance_scale"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestGenerateImages_URLFetchHappyPath(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")

	// A single fixture server plays both roles: the fal.run generation
	// endpoint and the host serving the generated image URL.
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/fal-ai/flux/schnell", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[{"url":"` + srv.URL + `/generated/img.png","content_type":"image/png"}]}`))
	})
	mux.HandleFunc("/generated/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(pngBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("len(Images) = %d, want 1", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(pngBytes) {
		t.Errorf("Data mismatch")
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_DataURLPath(t *testing.T) {
	raw := []byte("raw-image-bytes")
	dataURL := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(raw)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[{"url":"` + dataURL + `"}]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("len(Images) = %d", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(raw) {
		t.Errorf("Data = %q, want %q", resp.Images[0].Data, raw)
	}
	if resp.Images[0].MediaType != "image/webp" {
		t.Errorf("MediaType = %q, want image/webp", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_EmptyImagesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty images")
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.ResponseBody, "invalid api key") {
		t.Errorf("ResponseBody = %q", apiErr.ResponseBody)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "invalid api key")
	}
}

func TestGenerateImages_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"images":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("fal-ai/flux/schnell")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateImages(ctx, provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
