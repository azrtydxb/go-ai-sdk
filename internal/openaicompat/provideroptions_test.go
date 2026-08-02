package openaicompat

// Request-shape tests for ProviderOptions wiring: (a) an option key
// overriding an SDK-built field, (b) a novel passthrough key not otherwise
// exposed by this SDK. White-box (package openaicompat) since they decode
// the recorded raw request body directly.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestChatProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewLanguageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-test")

	temp := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:    []provider.Message{provider.UserText("simple")},
		Temperature: &temp,
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"temperature": 0.9,
				"logprobs":    true,
			},
			"other-provider": map[string]any{
				"temperature": 0.5,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var temperature float64
	if err := json.Unmarshal(raw["temperature"], &temperature); err != nil {
		t.Fatalf("decode temperature: %v", err)
	}
	if temperature != 0.9 {
		t.Errorf("temperature = %v, want provider option override 0.9", temperature)
	}

	logprobsRaw, ok := raw["logprobs"]
	if !ok {
		t.Fatalf("request missing novel passthrough key logprobs: %s", lastRawBody(srv))
	}
	var logprobs bool
	if err := json.Unmarshal(logprobsRaw, &logprobs); err != nil {
		t.Fatalf("decode logprobs: %v", err)
	}
	if !logprobs {
		t.Errorf("logprobs = %v, want true", logprobs)
	}
}

func TestChatProviderOptionsOtherProviderKeyIgnored(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewLanguageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-test")

	temp := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:    []provider.Message{provider.UserText("simple")},
		Temperature: &temp,
		ProviderOptions: map[string]any{
			"other-provider": map[string]any{
				"temperature": 0.5,
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(lastRawBody(srv), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var temperature float64
	if err := json.Unmarshal(raw["temperature"], &temperature); err != nil {
		t.Fatalf("decode temperature: %v", err)
	}
	if temperature != 0.1 {
		t.Errorf("temperature = %v, want unchanged 0.1 (other provider's options must be ignored)", temperature)
	}
}

func TestImageProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewImageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "dall-e-3")

	_, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt: "a cat",
		N:      2,
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"n":     1,
				"style": "vivid",
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var n float64
	if err := json.Unmarshal(raw["n"], &n); err != nil {
		t.Fatalf("decode n: %v", err)
	}
	if n != 1 {
		t.Errorf("n = %v, want provider option override 1", n)
	}
	styleRaw, ok := raw["style"]
	if !ok {
		t.Fatalf("request missing novel passthrough key style: %s", reqs[0])
	}
	var style string
	if err := json.Unmarshal(styleRaw, &style); err != nil {
		t.Fatalf("decode style: %v", err)
	}
	if style != "vivid" {
		t.Errorf("style = %q, want vivid", style)
	}
}

func TestSpeechProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewSpeechModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "tts-1")

	_, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hello",
		Voice: "alloy",
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"voice":                  "nova",
				"stream_output_channels": 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var voice string
	if err := json.Unmarshal(raw["voice"], &voice); err != nil {
		t.Fatalf("decode voice: %v", err)
	}
	if voice != "nova" {
		t.Errorf("voice = %q, want provider option override nova", voice)
	}
	if _, ok := raw["stream_output_channels"]; !ok {
		t.Errorf("request missing novel passthrough key stream_output_channels: %s", reqs[0])
	}
}

func TestTranscriptionProviderOptionsExtraFormField(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewTranscriptionModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"temperature": 0.5,
			},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	ct := srv.HeaderValues("Content-Type")[0]
	_, _, _, fields := parseMultipart(t, reqs[0], ct)

	if got := fields["temperature"]; got != "0.5" {
		t.Errorf("temperature form field = %q, want %q", got, "0.5")
	}
}

func TestEmbeddingProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewEmbeddingModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL, EmbedBatch: 100}, "text-embedding-3-small")

	m2, ok := model.(provider.EmbeddingModelV2)
	if !ok {
		t.Fatalf("openaicompat embeddingModel does not implement provider.EmbeddingModelV2")
	}

	_, err := m2.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values: []string{"a"},
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"model":           "text-embedding-3-large",
				"encoding_format": "base64",
			},
		},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(reqs[0], &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var model2 string
	if err := json.Unmarshal(raw["model"], &model2); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	if model2 != "text-embedding-3-large" {
		t.Errorf("model = %q, want provider option override text-embedding-3-large", model2)
	}
	if _, ok := raw["encoding_format"]; !ok {
		t.Errorf("request missing novel passthrough key encoding_format: %s", reqs[0])
	}
}
