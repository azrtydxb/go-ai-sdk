package openai

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranscriptionModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openai")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).TranscriptionModel("whisper-1")

	if got := model.ModelID(); got != "whisper-1" {
		t.Errorf("ModelID() = %q, want %q", got, "whisper-1")
	}
	if got := model.ProviderName(); got != "openai" {
		t.Errorf("ProviderName() = %q, want %q", got, "openai")
	}

	resp, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", resp.Text)
	}
	if len(resp.Segments) != 2 {
		t.Errorf("got %d segments, want 2", len(resp.Segments))
	}
}
