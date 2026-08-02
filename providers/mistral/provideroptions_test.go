package mistral

// Request-shape tests for ProviderOptions wiring: (a) an option key
// overriding an SDK-built field, (b) a novel passthrough key not otherwise
// exposed by this SDK.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestChatProviderOptionsOverridesAndPassthrough(t *testing.T) {
	srv, fs := newFixtureServer(t)
	defer srv.Close()
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

	temp := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:    []provider.Message{provider.UserText("simple")},
		Temperature: &temp,
		ProviderOptions: map[string]any{
			"mistral": map[string]any{
				"temperature": 0.9,
				"safe_prompt": true,
				"random_seed": 42,
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
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var temperature float64
	if err := json.Unmarshal(raw["temperature"], &temperature); err != nil {
		t.Fatalf("decode temperature: %v", err)
	}
	if temperature != 0.9 {
		t.Errorf("temperature = %v, want provider option override 0.9", temperature)
	}

	if _, ok := raw["safe_prompt"]; !ok {
		t.Errorf("request missing novel passthrough key safe_prompt: %s", fs.rawBody())
	}
	seedRaw, ok := raw["random_seed"]
	if !ok {
		t.Fatalf("request missing novel passthrough key random_seed: %s", fs.rawBody())
	}
	var seed float64
	if err := json.Unmarshal(seedRaw, &seed); err != nil {
		t.Fatalf("decode random_seed: %v", err)
	}
	if seed != 42 {
		t.Errorf("random_seed = %v, want 42", seed)
	}
}

func TestChatProviderOptionsOtherProviderKeyIgnored(t *testing.T) {
	srv, fs := newFixtureServer(t)
	defer srv.Close()
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("mistral-test")

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
	if err := json.Unmarshal(fs.rawBody(), &raw); err != nil {
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
