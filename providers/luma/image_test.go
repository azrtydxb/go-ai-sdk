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

func TestGenerateImages_RequestShape(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
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
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"image":"` + srv.URL + `/img.png"}}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("photon-1")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/dream-machine/v1/generations/image" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotBody["prompt"] != "a cat" {
		t.Errorf("prompt = %v", gotBody["prompt"])
	}
	if gotBody["model"] != "photon-1" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["aspect_ratio"] != "16:9" {
		t.Errorf("aspect_ratio = %v", gotBody["aspect_ratio"])
	}
	if len(resp.Images) != 1 || string(resp.Images[0].Data) != "pngdata" {
		t.Fatalf("unexpected images: %+v", resp.Images)
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_OmitsAspectRatioWhenEmpty(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"image":"` + srv.URL + `/img.png"}}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if _, ok := gotBody["aspect_ratio"]; ok {
		t.Errorf("aspect_ratio should be omitted when empty, got %v", gotBody["aspect_ratio"])
	}
}

func TestGenerateImages_PollHappyPath(t *testing.T) {
	var pollCount int32

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
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
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"image":"` + srv.URL + `/img.png"}}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("photon-1")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if atomic.LoadInt32(&pollCount) != 3 {
		t.Errorf("pollCount = %d, want 3 (2 pending + 1 completed)", pollCount)
	}
	if len(resp.Images) != 1 || string(resp.Images[0].Data) != "pngdata" {
		t.Fatalf("unexpected images: %+v", resp.Images)
	}
}

func TestGenerateImages_FailedState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
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
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for failed generation")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("error = %q, want it to include failure_reason", err.Error())
	}
}

func TestGenerateImages_EmptyCreateIDError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":""}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty generation id")
	}
	if !strings.Contains(err.Error(), "no generation id") {
		t.Errorf("error = %q, want it to mention the missing generation id", err.Error())
	}
}

func TestGenerateImages_PollNon2xxError(t *testing.T) {
	var pollHit int32

	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
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
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
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
	// The poll loop must exit on the first non-2xx response rather than
	// retrying indefinitely.
	if got := atomic.LoadInt32(&pollHit); got != 1 {
		t.Errorf("poll hit count = %d, want 1 (loop should exit on first error)", got)
	}
}

func TestGenerateImages_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
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

	// A poll interval long enough that the test can cancel the context
	// while GenerateImages is sleeping between polls.
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.ImageModel("photon-1")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-pollHit
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := m.GenerateImages(ctx, provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestGenerateImages_ProviderOptionsMergeTopLevel(t *testing.T) {
	var gotBody map[string]any

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/dream-machine/v1/generations/image", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1"}`))
	})
	mux.HandleFunc("/dream-machine/v1/generations/gen-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","state":"completed","assets":{"image":"` + srv.URL + `/img.png"}}`))
	})
	mux.HandleFunc("/img.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pngdata"))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
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
		t.Fatalf("GenerateImages: %v", err)
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

func TestGenerateImages_NGreaterThanOneError(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      2,
	})
	if err == nil {
		t.Fatal("expected error for N>1")
	}
	if err.Error() != "luma: multiple images per call are not supported" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGenerateImages_SizeUnsupported(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.ImageModel("photon-1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		Size:   "1024x1024",
	})
	if err == nil {
		t.Fatal("expected error for Size")
	}
	if err.Error() != "luma: size is not supported; use AspectRatio" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("photon-1")

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
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "invalid api key")
	}
}
