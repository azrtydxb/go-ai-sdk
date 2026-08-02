package openai

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestImageModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openai")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).ImageModel("gpt-image-1")

	if got := model.ModelID(); got != "gpt-image-1" {
		t.Errorf("ModelID() = %q, want %q", got, "gpt-image-1")
	}
	if got := model.ProviderName(); got != "openai" {
		t.Errorf("ProviderName() = %q, want %q", got, "openai")
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
}
