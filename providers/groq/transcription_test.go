package groq

import (
	"context"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranscriptionModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "groq")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).TranscriptionModel("whisper-large-v3")

	if got := model.ModelID(); got != "whisper-large-v3" {
		t.Errorf("ModelID() = %q, want %q", got, "whisper-large-v3")
	}
	if got := model.ProviderName(); got != "groq" {
		t.Errorf("ProviderName() = %q, want %q", got, "groq")
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

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d recorded requests, want 1", len(reqs))
	}
	// "whisper-large-v3" doesn't contain "gpt-4o", so the model always
	// requests verbose_json (not the gpt-4o "json"-only carve-out).
	if !strings.Contains(string(reqs[0]), "verbose_json") {
		t.Errorf("request body does not contain verbose_json, want response_format=verbose_json field")
	}
}
