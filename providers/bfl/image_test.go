package bfl

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

func TestGenerateImages_CreatePollSampleHappyPath(t *testing.T) {
	sampleBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")

	// newFixtureServer's /poll handler always points result.sample at this
	// same server's /sample path (see below), so pollBodies here only need
	// the status transitions; the sample URL is filled in dynamically.
	var srv *httptest.Server
	pollBodies := []string{
		`{"id":"gen-1","status":"Pending"}`,
		`{"id":"gen-1","status":"Ready","result":{"sample":"SAMPLE_URL"}}`,
	}

	mux := http.NewServeMux()
	var pollCount int32
	var gotCreateKey, gotPollKey string
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		gotCreateKey = r.Header.Get("x-key")
		io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		gotPollKey = r.Header.Get("x-key")
		n := atomic.AddInt32(&pollCount, 1)
		idx := int(n) - 1
		if idx >= len(pollBodies) {
			idx = len(pollBodies) - 1
		}
		body := strings.ReplaceAll(pollBodies[idx], "SAMPLE_URL", srv.URL+"/sample")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	resp, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		Size:   "1024x768",
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	if gotCreateKey != "test-key" {
		t.Errorf("create x-key = %q, want test-key", gotCreateKey)
	}
	if gotPollKey != "test-key" {
		t.Errorf("poll x-key = %q, want test-key", gotPollKey)
	}
	if atomic.LoadInt32(&pollCount) != 2 {
		t.Errorf("pollCount = %d, want 2", atomic.LoadInt32(&pollCount))
	}
	if len(resp.Images) != 1 {
		t.Fatalf("len(Images) = %d, want 1", len(resp.Images))
	}
	if string(resp.Images[0].Data) != string(sampleBytes) {
		t.Errorf("Data mismatch")
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", resp.Images[0].MediaType)
	}
}

func TestGenerateImages_WidthHeightFromSize(t *testing.T) {
	var gotBody map[string]any
	sampleBytes := []byte("bytes")

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"` + srv.URL + `/sample"}}`))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Size: "1024x768"})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotBody["width"] != float64(1024) {
		t.Errorf("width = %v, want 1024", gotBody["width"])
	}
	if gotBody["height"] != float64(768) {
		t.Errorf("height = %v, want 768", gotBody["height"])
	}
}

func TestGenerateImages_ErrorStatus(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Error"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for Error status")
	}
	if !strings.Contains(err.Error(), "Error") {
		t.Errorf("error = %q, want it to mention the status", err.Error())
	}
}

func TestGenerateImages_ContentModeratedStatus(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Content Moderated"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for Content Moderated status")
	}
	if !strings.Contains(err.Error(), "Content Moderated") {
		t.Errorf("error = %q, want it to mention the status", err.Error())
	}
}

func TestGenerateImages_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.ImageModel("flux-pro-1.1")

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
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerateImages_PollNon2xxError(t *testing.T) {
	var pollHit int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

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
	if got := atomic.LoadInt32(&pollHit); got != 1 {
		t.Errorf("poll hit count = %d, want 1 (loop should exit on first error)", got)
	}
}

func TestGenerateImages_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Pending"}`))
		select {
		case pollHit <- struct{}{}:
		default:
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// A poll interval long enough that the test can cancel the context
	// while GenerateImages is sleeping between polls.
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

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
	sampleBytes := []byte("bytes")

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/flux-pro-1.1", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":"` + srv.URL + `/poll"}`))
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","status":"Ready","result":{"sample":"` + srv.URL + `/sample"}}`))
	})
	mux.HandleFunc("/sample", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		w.Write(sampleBytes)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		ProviderOptions: map[string]any{
			"bfl": map[string]any{"safety_tolerance": 6},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotBody["safety_tolerance"] != float64(6) {
		t.Errorf("safety_tolerance = %v, want 6", gotBody["safety_tolerance"])
	}
}

func TestGenerateImages_EmptyPollingURLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-1","polling_url":""}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.ImageModel("flux-pro-1.1")

	_, err := m.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
	if err == nil {
		t.Fatal("expected error for empty polling_url")
	}
	if !strings.Contains(err.Error(), "polling_url") {
		t.Errorf("error = %q, want it to mention the missing polling_url", err.Error())
	}
}
