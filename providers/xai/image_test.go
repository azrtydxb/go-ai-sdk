package xai

import (
	"context"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestImageModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "xai")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).ImageModel("grok-2-image")

	if got := model.ModelID(); got != "grok-2-image" {
		t.Errorf("ModelID() = %q, want %q", got, "grok-2-image")
	}
	if got := model.ProviderName(); got != "xai" {
		t.Errorf("ProviderName() = %q, want %q", got, "xai")
	}

	resp, err := model.GenerateImages(context.Background(), provider.ImageCall{Prompt: "a cat"})
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

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d recorded requests, want 1", len(reqs))
	}
	if !strings.Contains(string(reqs[0]), `"response_format":"b64_json"`) {
		t.Errorf("request body = %s, want it to contain response_format:b64_json", reqs[0])
	}
}
