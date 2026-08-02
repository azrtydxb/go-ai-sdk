package ai

// Verifies GenerateImageOpts/GenerateSpeechOpts/TranscribeOpts.ProviderOptions
// thread through to the underlying provider.ImageCall/SpeechCall/
// TranscriptionCall unchanged.

import (
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateImageThreadsProviderOptions(t *testing.T) {
	opts := map[string]any{"openai": map[string]any{"style": "vivid"}}
	m := &aitest.MockImageModel{Response: &provider.ImageResponse{
		Images: []provider.GeneratedImage{{Data: []byte("x"), MediaType: "image/png"}},
	}}

	_, err := GenerateImage(t.Context(), GenerateImageOpts{Model: m, Prompt: "a cat", ProviderOptions: opts})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("Calls = %d, want 1", len(m.Calls))
	}
	if m.Calls[0].ProviderOptions["openai"] == nil {
		t.Errorf("ProviderOptions not threaded through: %+v", m.Calls[0].ProviderOptions)
	}
}

func TestGenerateSpeechThreadsProviderOptions(t *testing.T) {
	opts := map[string]any{"openai": map[string]any{"speed": 1.5}}
	m := &aitest.MockSpeechModel{Response: &provider.SpeechResponse{
		Audio: []byte("audio"), MediaType: "audio/mpeg",
	}}

	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Model: m, Text: "hi", ProviderOptions: opts})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("Calls = %d, want 1", len(m.Calls))
	}
	if m.Calls[0].ProviderOptions["openai"] == nil {
		t.Errorf("ProviderOptions not threaded through: %+v", m.Calls[0].ProviderOptions)
	}
}

func TestTranscribeThreadsProviderOptions(t *testing.T) {
	opts := map[string]any{"openai": map[string]any{"temperature": 0.2}}
	m := &aitest.MockTranscriptionModel{Response: &provider.TranscriptionResponse{Text: "hi"}}

	_, err := Transcribe(t.Context(), TranscribeOpts{Model: m, Audio: []byte("a"), ProviderOptions: opts})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("Calls = %d, want 1", len(m.Calls))
	}
	if m.Calls[0].ProviderOptions["openai"] == nil {
		t.Errorf("ProviderOptions not threaded through: %+v", m.Calls[0].ProviderOptions)
	}
}
