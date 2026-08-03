package fal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateVideos_RequestShapeAndSingleVideoResponse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/fal-ai/kling-video/v1/standard/text-to-video", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"video":{"url":"` + srv.URL + `/vid.mp4","content_type":"video/mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt:      "a cat running",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if gotPath != "/fal-ai/kling-video/v1/standard/text-to-video" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Key test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Key test-key")
	}
	if gotBody["prompt"] != "a cat running" {
		t.Errorf("prompt = %v", gotBody["prompt"])
	}
	if gotBody["aspect_ratio"] != "16:9" {
		t.Errorf("aspect_ratio = %v", gotBody["aspect_ratio"])
	}
	if len(resp.Videos) != 1 || string(resp.Videos[0].Data) != "mp4data" {
		t.Fatalf("unexpected videos: %+v", resp.Videos)
	}
	if resp.Videos[0].MediaType != "video/mp4" {
		t.Errorf("MediaType = %q", resp.Videos[0].MediaType)
	}
}

func TestGenerateVideos_AspectRatioOmittedWhenEmpty(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/fal-ai/kling-video/v1/standard/text-to-video", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"video":{"url":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if _, ok := gotBody["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be omitted when empty, got %v", gotBody["aspect_ratio"])
	}
}

func TestGenerateVideos_VideosArrayResponse(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/fal-ai/kling-video/v1/standard/text-to-video", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"videos":[{"url":"` + srv.URL + `/v1.mp4"},{"url":"` + srv.URL + `/v2.mp4"}]}`))
	})
	mux.HandleFunc("/v1.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4-one"))
	})
	mux.HandleFunc("/v2.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4-two"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}
	if len(resp.Videos) != 2 {
		t.Fatalf("len(Videos) = %d, want 2", len(resp.Videos))
	}
	if string(resp.Videos[0].Data) != "mp4-one" || string(resp.Videos[1].Data) != "mp4-two" {
		t.Errorf("videos = %+v", resp.Videos)
	}
}

func TestGenerateVideos_ProviderOptionsMergeTopLevel(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/fal-ai/kling-video/v1/standard/text-to-video", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"video":{"url":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"fal": map[string]any{
				"duration":   "10",
				"resolution": "720p",
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if gotBody["duration"] != "10" {
		t.Errorf("duration = %v, want passthrough", gotBody["duration"])
	}
	if gotBody["resolution"] != "720p" {
		t.Errorf("resolution = %v, want passthrough", gotBody["resolution"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestGenerateVideos_EmptyVideosError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"videos":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty videos")
	}
}

func TestGenerateVideos_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
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
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "invalid api key")
	}
}

func TestGenerateVideos_429Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"detail":"rate limited"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if !apiErr.Retryable {
		t.Error("429 should be classified as retryable")
	}
}

func TestGenerateVideos_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"videos":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("fal-ai/kling-video/v1/standard/text-to-video")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateVideos(ctx, provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
