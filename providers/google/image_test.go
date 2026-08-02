package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

const onePixelPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type fixtureImagenRequest struct {
	Instances []struct {
		Prompt string `json:"prompt"`
	} `json:"instances"`
	Parameters struct {
		SampleCount int    `json:"sampleCount"`
		AspectRatio string `json:"aspectRatio"`
		Seed        int64  `json:"seed"`
	} `json:"parameters"`
}

func newImageFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "k" {
			t.Errorf("x-goog-api-key header = %q, want %q", got, "k")
		}
		if !strings.HasSuffix(r.URL.Path, "/models/imagen-3.0-generate-002:predict") {
			t.Errorf("path = %q, want suffix .../models/imagen-3.0-generate-002:predict", r.URL.Path)
		}

		var req fixtureImagenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if len(req.Instances) != 1 || req.Instances[0].Prompt != "a cat" {
			t.Errorf("instances = %+v, want one instance with prompt %q", req.Instances, "a cat")
		}
		if req.Parameters.SampleCount != 3 {
			t.Errorf("sampleCount = %d, want 3", req.Parameters.SampleCount)
		}
		if req.Parameters.Seed != 7 {
			t.Errorf("seed = %d, want 7", req.Parameters.Seed)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + onePixelPNGBase64 + `","mimeType":"image/png"}]}`))
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestImageModel(t *testing.T) {
	srv := newImageFixtureServer(t)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).ImageModel("imagen-3.0-generate-002")

	if got := model.ModelID(); got != "imagen-3.0-generate-002" {
		t.Errorf("ModelID() = %q, want %q", got, "imagen-3.0-generate-002")
	}
	if got := model.ProviderName(); got != "google" {
		t.Errorf("ProviderName() = %q, want %q", got, "google")
	}

	seed := int64(7)
	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      3,
		Seed:   &seed,
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if len(resp.Images) != 1 {
		t.Fatalf("got %d images, want 1", len(resp.Images))
	}
	if resp.Images[0].MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", resp.Images[0].MediaType)
	}
	if len(resp.Images[0].Data) == 0 {
		t.Error("Data is empty, want decoded PNG bytes")
	}
}

func TestImageModel_SizeUnsupported(t *testing.T) {
	model := New(WithAPIKey("k"), WithBaseURL("http://unused.invalid")).ImageModel("imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Size: "1024x1024"})
	if err == nil {
		t.Fatal("GenerateImages: want error when Size is set, got nil")
	}
	if err.Error() != "google: size is not supported; use AspectRatio" {
		t.Errorf("error = %q, want %q", err.Error(), "google: size is not supported; use AspectRatio")
	}
}
