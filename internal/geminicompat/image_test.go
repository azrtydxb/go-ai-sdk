package geminicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestImageModel_RequestShape(t *testing.T) {
	const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	var gotBody []byte
	var gotPath string
	var gotAuthHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthHeader = r.Header.Get("x-goog-api-key")
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = body

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + onePixelPNGBase64 + `","mimeType":"image/png"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name: "google",
		EndpointFor: func(modelID, method string) string {
			return srv.URL + "/models/" + modelID + ":" + method
		},
		Authorize: func(ctx context.Context, req *http.Request) error {
			req.Header.Set("x-goog-api-key", "k")
			return nil
		},
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	if got := model.ModelID(); got != "imagen-3.0-generate-002" {
		t.Errorf("ModelID() = %q, want %q", got, "imagen-3.0-generate-002")
	}
	if got := model.ProviderName(); got != "google" {
		t.Errorf("ProviderName() = %q, want %q", got, "google")
	}

	seed := int64(42)
	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		N:           2,
		AspectRatio: "16:9",
		Seed:        &seed,
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if !strings.HasSuffix(gotPath, "/models/imagen-3.0-generate-002:predict") {
		t.Errorf("path = %q, want suffix .../models/imagen-3.0-generate-002:predict", gotPath)
	}
	if gotAuthHeader != "k" {
		t.Errorf("auth header = %q, want %q", gotAuthHeader, "k")
	}

	var req struct {
		Instances []struct {
			Prompt string `json:"prompt"`
		} `json:"instances"`
		Parameters struct {
			SampleCount int    `json:"sampleCount"`
			AspectRatio string `json:"aspectRatio"`
			Seed        int64  `json:"seed"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode request body: %v (body=%s)", err, gotBody)
	}
	if len(req.Instances) != 1 || req.Instances[0].Prompt != "a cat" {
		t.Errorf("instances = %+v, want one instance with prompt %q", req.Instances, "a cat")
	}
	if req.Parameters.SampleCount != 2 {
		t.Errorf("sampleCount = %d, want 2", req.Parameters.SampleCount)
	}
	if req.Parameters.AspectRatio != "16:9" {
		t.Errorf("aspectRatio = %q, want %q", req.Parameters.AspectRatio, "16:9")
	}
	if req.Parameters.Seed != 42 {
		t.Errorf("seed = %d, want 42", req.Parameters.Seed)
	}

	if len(resp.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Images))
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", resp.Images[0].MediaType)
	}
	wantData, _ := base64.StdEncoding.DecodeString(onePixelPNGBase64)
	if string(resp.Images[0].Data) != string(wantData) {
		t.Error("Data does not match decoded bytesBase64Encoded")
	}
}

func TestImageModel_DefaultSampleCount(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	if _, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"}); err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	var req struct {
		Parameters struct {
			SampleCount int `json:"sampleCount"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if req.Parameters.SampleCount != 1 {
		t.Errorf("sampleCount = %d, want default 1", req.Parameters.SampleCount)
	}
}

func TestImageModel_SizeUnsupported(t *testing.T) {
	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return "http://unused.invalid" },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Size: "1024x1024"})
	if err == nil {
		t.Fatal("GenerateImages: want error when Size is set, got nil")
	}
	if err.Error() != "google: size is not supported; use AspectRatio" {
		t.Errorf("error = %q, want %q", err.Error(), "google: size is not supported; use AspectRatio")
	}
}

func TestImageModel_EmptyBytesBase64Encoded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"","mimeType":"image/png"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("GenerateImages: want error when bytesBase64Encoded is empty, got nil")
	}
	if err.Error() != "google: image response missing image data" {
		t.Errorf("error = %q, want %q", err.Error(), "google: image response missing image data")
	}
}

func TestImageModel_InvalidBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"!!!","mimeType":"image/png"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("GenerateImages: want error when bytesBase64Encoded is invalid base64, got nil")
	}
	if !strings.Contains(err.Error(), "decode image bytesBase64Encoded") {
		t.Errorf("error = %q, want it to mention decode failure", err.Error())
	}
}

func TestImageModel_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("GenerateImages: want error on 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "bad request")
	}
}

func TestImageModel_SniffsMediaTypeWhenMimeTypeAbsent(t *testing.T) {
	const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No mimeType field: the model must sniff the decoded bytes' magic
		// bytes to determine the MediaType.
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + onePixelPNGBase64 + `"}]}`))
	}))
	defer srv.Close()

	cfg := Config{
		Name:        "google",
		EndpointFor: func(modelID, method string) string { return srv.URL },
		Authorize:   func(ctx context.Context, req *http.Request) error { return nil },
	}
	model := NewImageModel(cfg, "imagen-3.0-generate-002")

	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Images))
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png (sniffed)", resp.Images[0].MediaType)
	}
}
