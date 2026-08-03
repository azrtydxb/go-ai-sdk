package replicate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateVideos_RequestShape(t *testing.T) {
	var gotPath, gotAuth, gotPrefer string
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotPrefer = r.Header.Get("Prefer")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/vid.mp4"}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-token"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt:      "a cat running",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if gotPath != "/v1/models/minimax/video-01/predictions" {
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
	if input["prompt"] != "a cat running" {
		t.Errorf("input.prompt = %v", input["prompt"])
	}
	if input["aspect_ratio"] != "16:9" {
		t.Errorf("input.aspect_ratio = %v", input["aspect_ratio"])
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
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/vid.mp4"}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	input, _ := gotBody["input"].(map[string]any)
	if _, ok := input["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be omitted when empty, got %v", input["aspect_ratio"])
	}
}

func TestGenerateVideos_ProviderOptionsMergeIntoInput(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"` + srv.URL + `/vid.mp4"}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"replicate": map[string]any{
				"prompt":   "overridden prompt",
				"duration": float64(5),
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	input, _ := gotBody["input"].(map[string]any)
	if input == nil {
		t.Fatalf("body.input missing: %v", gotBody)
	}
	if input["prompt"] != "overridden prompt" {
		t.Errorf("input.prompt = %v, want override", input["prompt"])
	}
	if input["duration"] != float64(5) {
		t.Errorf("input.duration = %v, want passthrough 5", input["duration"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
	if _, ok := input["other-provider"]; ok {
		t.Error("other-provider options should not leak into input")
	}
}

func TestGenerateVideos_URLFetchHappyPathSingleAndArray(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":["` + srv.URL + `/v1.mp4","` + srv.URL + `/v2.mp4"]}`))
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
	m := p.VideoModel("minimax/video-01")

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

func TestGenerateVideos_FetchVideoErrorIsSinglePrefixed(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":["` + srv.URL + `/missing.mp4"]}`))
	})
	mux.HandleFunc("/missing.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for failed video fetch")
	}
	msg := err.Error()
	if strings.Count(msg, "replicate:") != 1 {
		t.Errorf("error = %q, want exactly one %q prefix (no double-wrap like the old replicate: fetch video: replicate: fetch)", msg, "replicate:")
	}
	if strings.Count(msg, "fetch ") != 1 {
		t.Errorf("error = %q, want exactly one \"fetch \" (no double-wrap)", msg)
	}
}

func TestGenerateVideos_FailedStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"failed","error":"NSFW content detected"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
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

func TestGenerateVideos_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"Invalid token."}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-token"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

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
	if apiErr.Message != "Invalid token." {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "Invalid token.")
	}
}

func TestGenerateVideos_429Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"detail":"rate limited"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

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

// --- Fix wave IMPORTANT 3 — "Prefer: wait" alone fails typical multi-minute
// video generations; poll GET /v1/predictions/{id} until a terminal status
// instead of erroring (and risking a caller retry that creates a second,
// paid prediction). ---

func TestGenerateVideos_ProcessingStatusPollsUntilSucceeded(t *testing.T) {
	var pollCount atomic.Int32
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"pred-1","status":"starting"}`))
	})
	mux.HandleFunc("/v1/predictions/pred-1", func(w http.ResponseWriter, r *http.Request) {
		n := pollCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if n < 3 {
			w.Write([]byte(`{"id":"pred-1","status":"processing"}`))
			return
		}
		w.Write([]byte(`{"id":"pred-1","status":"succeeded","output":"` + srv.URL + `/vid.mp4"}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("minimax/video-01")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}
	if pollCount.Load() != 3 {
		t.Errorf("pollCount = %d, want 3", pollCount.Load())
	}
	if len(resp.Videos) != 1 || string(resp.Videos[0].Data) != "mp4data" {
		t.Fatalf("unexpected videos: %+v", resp.Videos)
	}
}

func TestGenerateVideos_PollFailedStatusError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"pred-1","status":"processing"}`))
	})
	mux.HandleFunc("/v1/predictions/pred-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"pred-1","status":"failed","error":"NSFW content detected"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("minimax/video-01")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for failed status reached via polling")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("error = %q, want it to mention status", err.Error())
	}
	if !strings.Contains(err.Error(), "NSFW content detected") {
		t.Errorf("error = %q, want it to mention the error field", err.Error())
	}
}

func TestGenerateVideos_CtxCancelMidPoll(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models/minimax/video-01/predictions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"pred-1","status":"processing"}`))
	})
	mux.HandleFunc("/v1/predictions/pred-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"pred-1","status":"processing"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A poll interval long enough that the ctx will be cancelled while
	// sleeping between polls, not while an HTTP call is in flight.
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.VideoModel("minimax/video-01")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := m.GenerateVideos(ctx, provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for context cancelled mid-poll")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestGenerateVideos_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"succeeded","output":"https://example.test/vid.mp4"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("minimax/video-01")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateVideos(ctx, provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
