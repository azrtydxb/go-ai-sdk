// Package ai_test holds registry tests in an external test package: the
// registry tests exercise real provider packages (openai, bedrock), and
// some of those providers import ai itself (e.g. bedrock's language model
// uses ai's tool-loop helpers), which would create an import cycle if this
// file lived in package ai.
package ai_test

import (
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/bedrock"
	"github.com/azrtydxb/go-ai-sdk/providers/cohere"
	"github.com/azrtydxb/go-ai-sdk/providers/luma"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

func TestRegistry_LanguageModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	m, err := r.LanguageModel("openai:gpt-4o")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "gpt-4o" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "gpt-4o")
	}
}

func TestRegistry_EmbeddingModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	m, err := r.EmbeddingModel("openai:text-embedding-3-small")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "text-embedding-3-small" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "text-embedding-3-small")
	}
}

func TestRegistry_ImageModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	m, err := r.ImageModel("openai:gpt-image-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "gpt-image-1" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "gpt-image-1")
	}
}

func TestRegistry_SpeechModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	m, err := r.SpeechModel("openai:tts-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "tts-1" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "tts-1")
	}
}

func TestRegistry_TranscriptionModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	m, err := r.TranscriptionModel("openai:whisper-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "whisper-1" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "whisper-1")
	}
}

func TestRegistry_RerankingModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("cohere", cohere.New())

	m, err := r.RerankingModel("cohere:rerank-v3.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "rerank-v3.5" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "rerank-v3.5")
	}
}

func TestRegistry_VideoModel(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("luma", luma.New())

	m, err := r.VideoModel("luma:ray-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "ray-2" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "ray-2")
	}
}

func TestRegistry_InvalidModelID(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	_, err := r.LanguageModel("no-colon-here")
	if err == nil {
		t.Fatal("expected error")
	}
	want := `ai: invalid model id "no-colon-here" (want "provider:model")`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestRegistry_UnknownProvider(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("openai", openai.New())

	_, err := r.LanguageModel("nope:gpt-4o")
	if err == nil {
		t.Fatal("expected error")
	}
	want := `ai: unknown provider "nope"`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestRegistry_CapabilityNotSupported(t *testing.T) {
	r := ai.NewRegistry()
	// bedrock supports LanguageModel + EmbeddingModel, but not
	// image/speech/transcription.
	r.Register("bedrock", bedrock.New())

	tests := []struct {
		name string
		call func() error
		want string
	}{
		{
			"image",
			func() error { _, err := r.ImageModel("bedrock:foo"); return err },
			`ai: provider "bedrock" does not support image models`,
		},
		{
			"speech",
			func() error { _, err := r.SpeechModel("bedrock:foo"); return err },
			`ai: provider "bedrock" does not support speech models`,
		},
		{
			"transcription",
			func() error { _, err := r.TranscriptionModel("bedrock:foo"); return err },
			`ai: provider "bedrock" does not support transcription models`,
		},
		{
			"reranking",
			func() error { _, err := r.RerankingModel("bedrock:foo"); return err },
			`ai: provider "bedrock" does not support reranking models`,
		},
		{
			"video",
			func() error { _, err := r.VideoModel("bedrock:foo"); return err },
			`ai: provider "bedrock" does not support video models`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.want {
				t.Errorf("err = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestRegistry_LanguageModelNotSupported(t *testing.T) {
	r := ai.NewRegistry()
	// Register something that doesn't implement LanguageModelProvider at
	// all, e.g. an EmbeddingModelProvider-only fake.
	r.Register("embedonly", embedOnlyProvider{})

	_, err := r.LanguageModel("embedonly:foo")
	if err == nil {
		t.Fatal("expected error")
	}
	want := `ai: provider "embedonly" does not support language models`
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

type embedOnlyProvider struct{}

func (embedOnlyProvider) EmbeddingModel(id string) provider.EmbeddingModel { return nil }

func TestRegistry_ColonInModelID(t *testing.T) {
	r := ai.NewRegistry()
	r.Register("bedrock", bedrock.New())

	m, err := r.LanguageModel("bedrock:anthropic.claude-3:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ModelID() != "anthropic.claude-3:1" {
		t.Errorf("ModelID() = %q, want %q", m.ModelID(), "anthropic.claude-3:1")
	}
	if !strings.Contains(m.ModelID(), ":") {
		t.Error("expected model id to retain colon")
	}
}
