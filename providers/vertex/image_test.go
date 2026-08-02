package vertex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func newImageFixtureServer(t *testing.T, wantBearer string) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantBearer {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer "+wantBearer)
		}
		wantPath := fmt.Sprintf("/projects/%s/locations/%s/publishers/google/models/%s:predict", testProject, testLocation, "imagen-3.0-generate-002")
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		var req fixtureImagenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if len(req.Instances) != 1 || req.Instances[0].Prompt != "a cat" {
			t.Errorf("instances = %+v, want one instance with prompt %q", req.Instances, "a cat")
		}
		if req.Parameters.SampleCount != 1 {
			t.Errorf("sampleCount = %d, want 1", req.Parameters.SampleCount)
		}
		if req.Parameters.AspectRatio != "16:9" {
			t.Errorf("aspectRatio = %q, want %q", req.Parameters.AspectRatio, "16:9")
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"predictions":[{"bytesBase64Encoded":%q,"mimeType":"image/png"}]}`, onePixelPNGBase64)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestImageModel(t *testing.T) {
	const token = "image-test-token"
	srv := newImageFixtureServer(t, token)
	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL(srv.URL),
		WithAccessToken(token),
	)
	model := p.ImageModel("imagen-3.0-generate-002")

	if got := model.ModelID(); got != "imagen-3.0-generate-002" {
		t.Errorf("ModelID() = %q, want %q", got, "imagen-3.0-generate-002")
	}
	if got := model.ProviderName(); got != "vertex" {
		t.Errorf("ProviderName() = %q, want %q", got, "vertex")
	}

	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:      "a cat",
		AspectRatio: "16:9",
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
	p := New(
		WithProject(testProject),
		WithLocation(testLocation),
		WithBaseURL("http://unused.invalid"),
		WithAccessToken("t"),
	)
	model := p.ImageModel("imagen-3.0-generate-002")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat", Size: "1024x1024"})
	if err == nil {
		t.Fatal("GenerateImages: want error when Size is set, got nil")
	}
	if err.Error() != "vertex: size is not supported; use AspectRatio" {
		t.Errorf("error = %q, want %q", err.Error(), "vertex: size is not supported; use AspectRatio")
	}
}
