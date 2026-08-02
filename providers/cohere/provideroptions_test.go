package cohere

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
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("command-test")

	topP := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		TopP:     &topP,
		ProviderOptions: map[string]any{
			"cohere": map[string]any{
				"p":                 0.9,
				"frequency_penalty": 0.5,
			},
			"other-provider": map[string]any{
				"p": 0.2,
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

	var p float64
	if err := json.Unmarshal(raw["p"], &p); err != nil {
		t.Fatalf("decode p: %v", err)
	}
	if p != 0.9 {
		t.Errorf("p = %v, want provider option override 0.9", p)
	}

	fpRaw, ok := raw["frequency_penalty"]
	if !ok {
		t.Fatalf("request missing novel passthrough key frequency_penalty: %s", fs.rawBody())
	}
	var fp float64
	if err := json.Unmarshal(fpRaw, &fp); err != nil {
		t.Fatalf("decode frequency_penalty: %v", err)
	}
	if fp != 0.5 {
		t.Errorf("frequency_penalty = %v, want 0.5", fp)
	}
}

func TestChatProviderOptionsOtherProviderKeyIgnored(t *testing.T) {
	srv, fs := newFixtureServer(t)
	defer srv.Close()
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("command-test")

	topP := 0.1
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
		TopP:     &topP,
		ProviderOptions: map[string]any{
			"other-provider": map[string]any{
				"p": 0.9,
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
	var p float64
	if err := json.Unmarshal(raw["p"], &p); err != nil {
		t.Fatalf("decode p: %v", err)
	}
	if p != 0.1 {
		t.Errorf("p = %v, want unchanged 0.1 (other provider's options must be ignored)", p)
	}
}
