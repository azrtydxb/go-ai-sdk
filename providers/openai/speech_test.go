package openai

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestSpeechModel(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openai")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).SpeechModel("tts-1")

	if got := model.ModelID(); got != "tts-1" {
		t.Errorf("ModelID() = %q, want %q", got, "tts-1")
	}
	if got := model.ProviderName(); got != "openai" {
		t.Errorf("ProviderName() = %q, want %q", got, "openai")
	}

	resp, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hello"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if string(resp.Audio) != "FAKEAUDIO" {
		t.Errorf("Audio = %q, want FAKEAUDIO", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q, want audio/mpeg", resp.MediaType)
	}
}
