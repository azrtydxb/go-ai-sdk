package prodia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateImages_RequestShapeAndBytesPassthrough(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotBody map[string]any
	imgBytes := []byte("fake-jpeg-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(imgBytes)
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		Size:   "512x512",
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotPath != "/job" {
		t.Errorf("path = %q, want /job", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotAccept != "image/jpeg" {
		t.Errorf("Accept = %q, want image/jpeg", gotAccept)
	}
	if gotBody["type"] != "inference.flux.schnell.txt2img" {
		t.Errorf("type = %v", gotBody["type"])
	}
	config, ok := gotBody["config"].(map[string]any)
	if !ok {
		t.Fatalf("config = %v (%T), want object", gotBody["config"], gotBody["config"])
	}
	if config["prompt"] != "a cat" {
		t.Errorf("config.prompt = %v", config["prompt"])
	}
	if config["width"] != float64(512) {
		t.Errorf("config.width = %v, want 512", config["width"])
	}
	if config["height"] != float64(512) {
		t.Errorf("config.height = %v, want 512", config["height"])
	}

	if len(resp.Images) != 1 {
		t.Fatalf("len(Images) = %d, want 1", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(imgBytes) {
		t.Errorf("Data mismatch")
	}
	if resp.Images[0].MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_SeedOmittedWhenNil(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	config := gotBody["config"].(map[string]any)
	if _, ok := config["seed"]; ok {
		t.Errorf("seed should be omitted when nil, got %v", config["seed"])
	}
	if _, ok := config["width"]; ok {
		t.Errorf("width should be omitted when Size is empty, got %v", config["width"])
	}
}

func TestGenerateImages_Seed(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	seed := int64(99)
	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Seed: &seed})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	config := gotBody["config"].(map[string]any)
	if config["seed"] != float64(99) {
		t.Errorf("seed = %v, want 99", config["seed"])
	}
}

func TestGenerateImages_ProviderOptionsMergeIntoConfig(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"prodia": map[string]any{
				"prompt":       "overridden prompt",
				"cfg_scale":    7.5,
				"sampler":      "DPM++ 2M Karras",
				"aspect_ratio": "square",
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	config := gotBody["config"].(map[string]any)
	if config["prompt"] != "overridden prompt" {
		t.Errorf("prompt = %v, want override", config["prompt"])
	}
	if config["cfg_scale"] != 7.5 {
		t.Errorf("cfg_scale = %v, want 7.5", config["cfg_scale"])
	}
	if config["sampler"] != "DPM++ 2M Karras" {
		t.Errorf("sampler = %v", config["sampler"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*ai.APICallError)
	if !ok {
		t.Fatalf("expected *ai.APICallError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerateImages_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("inference.flux.schnell.txt2img")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateImages(ctx, provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
