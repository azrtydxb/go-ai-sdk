package replicate

import (
	"context"
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
	var gotPath, gotAuth, gotPrefer string
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/black-forest-labs/flux-schnell/predictions", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPrefer = r.Header.Get("Prefer")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/img.png"}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-token"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	seed := int64(7)
	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		N:           3,
		AspectRatio: "16:9",
		Seed:        &seed,
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotPath != "/v1/models/black-forest-labs/flux-schnell/predictions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
	if gotPrefer != "wait" {
		t.Errorf("Prefer = %q, want wait", gotPrefer)
	}

	input, _ := gotBody["input"].(map[string]any)
	if input == nil {
		t.Fatalf("body.input missing: %v", gotBody)
	}
	if input["prompt"] != "a cat" {
		t.Errorf("input.prompt = %v", input["prompt"])
	}
	if input["num_outputs"] != float64(3) {
		t.Errorf("input.num_outputs = %v, want 3", input["num_outputs"])
	}
	if input["aspect_ratio"] != "16:9" {
		t.Errorf("input.aspect_ratio = %v", input["aspect_ratio"])
	}
	if input["seed"] != float64(7) {
		t.Errorf("input.seed = %v, want 7", input["seed"])
	}
}

func TestGenerateImages_OmitsZeroValues(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/black-forest-labs/flux-schnell/predictions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/img.png"}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	input, _ := gotBody["input"].(map[string]any)
	if _, ok := input["num_outputs"]; ok {
		t.Errorf("num_outputs should be omitted when 0, got %v", input["num_outputs"])
	}
	if _, ok := input["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be omitted when empty, got %v", input["aspect_ratio"])
	}
	if _, ok := input["seed"]; ok {
		t.Errorf("seed should be omitted when nil, got %v", input["seed"])
	}
}

func TestGenerateImages_SizeUnsupported(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		Size:   "1024x1024",
	})
	if err == nil {
		t.Fatal("expected error for Size")
	}
	if err.Error() != "replicate: size is not supported; use AspectRatio" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGenerateImages_ProviderOptionsMergeIntoInput(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/black-forest-labs/flux-schnell/predictions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/img.png"}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"replicate": map[string]any{
				"prompt":              "overridden prompt",
				"guidance":            3.5,
				"num_inference_steps": 4,
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	input, _ := gotBody["input"].(map[string]any)
	if input == nil {
		t.Fatalf("body.input missing: %v", gotBody)
	}
	if input["prompt"] != "overridden prompt" {
		t.Errorf("input.prompt = %v, want override", input["prompt"])
	}
	if input["guidance"] != 3.5 {
		t.Errorf("input.guidance = %v, want passthrough 3.5", input["guidance"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
	if _, ok := input["other-provider"]; ok {
		t.Error("other-provider options should not leak into input")
	}
}

func TestGenerateImages_URLFetchHappyPathSingleAndArray(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	jpegBytes := []byte("\xFF\xD8\xFFfake-jpeg-bytes")

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/black-forest-labs/flux-schnell/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":["` + srv.URL + `/img1.png","` + srv.URL + `/img2.jpg"]}`))
	})
	mux.HandleFunc("/img1.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(pngBytes)
	})
	mux.HandleFunc("/img2.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(jpegBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 2 {
		t.Fatalf("len(Images) = %d, want 2", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(pngBytes) || resp.Images[0].MediaType != "image/png" {
		t.Errorf("image[0] mismatch: %q %q", resp.Images[0].Data, resp.Images[0].MediaType)
	}
	if string(resp.Images[1].Data) != string(jpegBytes) || resp.Images[1].MediaType != "image/jpeg" {
		t.Errorf("image[1] mismatch: %q %q", resp.Images[1].Data, resp.Images[1].MediaType)
	}
}

func TestGenerateImages_FailedStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"failed","error":"NSFW content detected"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for failed status")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error = %q, want it to mention status", err.Error())
	}
	if !strings.Contains(err.Error(), "NSFW content detected") {
		t.Errorf("error = %q, want it to mention the error field", err.Error())
	}
}

func TestGenerateImages_ProcessingStatusErrorNoErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"processing"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for non-succeeded status")
	}
	if !strings.Contains(err.Error(), "processing") {
		t.Errorf("error = %q, want it to mention status", err.Error())
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"Invalid token."}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-token"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

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
	if !strings.Contains(apiErr.ResponseBody, "Invalid token.") {
		t.Errorf("ResponseBody = %q", apiErr.ResponseBody)
	}
	if apiErr.Message != "Invalid token." {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "Invalid token.")
	}
}

func TestGenerateImages_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"data:image/png;base64,ZA=="}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("black-forest-labs/flux-schnell")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateImages(ctx, provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
