package luma

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
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"video":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt:      "a cat running",
		AspectRatio: "16:9",
		Resolution:  "720p",
		DurationSec: 5,
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/dream-machine/v1/generations" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotBody["prompt"] != "a cat running" {
		t.Errorf("prompt = %v", gotBody["prompt"])
	}
	if gotBody["model"] != "ray-2" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["aspect_ratio"] != "16:9" {
		t.Errorf("aspect_ratio = %v", gotBody["aspect_ratio"])
	}
	if gotBody["resolution"] != "720p" {
		t.Errorf("resolution = %v", gotBody["resolution"])
	}
	if gotBody["duration"] != "5s" {
		t.Errorf("duration = %v, want 5s", gotBody["duration"])
	}
	if len(resp.Videos) != 1 || string(resp.Videos[0].Data) != "mp4data" {
		t.Fatalf("unexpected videos: %+v", resp.Videos)
	}
	if resp.Videos[0].MediaType != "video/mp4" {
		t.Errorf("MediaType = %q", resp.Videos[0].MediaType)
	}
	if resp.Videos[0].URL != srv.URL+"/vid.mp4" {
		t.Errorf("URL = %q", resp.Videos[0].URL)
	}
}

func TestGenerateVideos_OmitsFieldsWhenEmpty(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"video":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if _, ok := gotBody["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be omitted when empty, got %v", gotBody["aspect_ratio"])
	}
	if _, ok := gotBody["resolution"]; ok {
		t.Errorf("resolution should be omitted when empty, got %v", gotBody["resolution"])
	}
	if _, ok := gotBody["duration"]; ok {
		t.Errorf("duration should be omitted when 0, got %v", gotBody["duration"])
	}
}

func TestGenerateVideos_PollHappyPath(t *testing.T) {
	var pollCount int32

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if n <= 2 {
			w.Write([]byte(`{"id":"gen-1","state":"pending"}`))
			return
		}
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"video":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	resp, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}
	if atomic.LoadInt32(&pollCount) != 3 {
		t.Errorf("pollCount = %d, want 3 (2 pending + 1 completed)", pollCount)
	}
	if len(resp.Videos) != 1 || string(resp.Videos[0].Data) != "mp4data" {
		t.Fatalf("unexpected videos: %+v", resp.Videos)
	}
}

func TestGenerateVideos_FailedState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"failed","failure_reason":"content policy violation"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for failed generation")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("error = %q, want it to include failure_reason", err.Error())
	}
}

func TestGenerateVideos_PollNon2xxError(t *testing.T) {
	var pollHit int32

	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"detail":"internal error"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for non-2xx poll response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if got := atomic.LoadInt32(&pollHit); got != 1 {
		t.Errorf("poll hit count = %d, want 1 (loop should exit on first error)", got)
	}
}

func TestGenerateVideos_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"pending"}`))
		select {
		case pollHit <- struct{}{}:
		default:
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.VideoModel("ray-2")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-pollHit
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := m.GenerateVideos(ctx, provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestGenerateVideos_ProviderOptionsMergeTopLevel(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"video":"` + srv.URL + `/vid.mp4"}}`))
	})
	mux.HandleFunc("/vid.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mp4data"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"luma": map[string]any{
				"prompt":       "overridden prompt",
				"callback_url": "https://example.test/hook",
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateVideos: %v", err)
	}

	if gotBody["prompt"] != "overridden prompt" {
		t.Errorf("prompt = %v, want override", gotBody["prompt"])
	}
	if gotBody["callback_url"] != "https://example.test/hook" {
		t.Errorf("callback_url = %v, want passthrough", gotBody["callback_url"])
	}
	if _, ok := gotBody["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestGenerateVideos_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.VideoModel("ray-2")

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
		t.Errorf("Message = %q, want parsed error %q", apiErr.Message, "invalid api key")
	}
}

func TestGenerateVideos_429Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"detail":"rate limited"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.VideoModel("ray-2")

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

func TestGenerateVideos_EmptyCreateIDError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":""}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.VideoModel("ray-2")

	_, err := m.GenerateVideos(context.Background(), provider.VideoCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty generation id")
	}
	if !strings.Contains(err.Error(), "no generation id") {
		t.Errorf("error = %q, want it to mention the missing generation id", err.Error())
	}
}
