package bedrock

// Request-shape tests for ProviderOptions wiring: (a) an option key
// overriding an SDK-built field, (b) a novel passthrough key not otherwise
// exposed by this SDK (additionalModelRequestFields, set wholesale).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestChatProviderOptionsOverridesAndPassthrough(t *testing.T) {
	model, fs := newTestModel(t)

	maxTokens := 7
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		MaxTokens: &maxTokens,
		ProviderOptions: map[string]any{
			"bedrock": map[string]any{
				"inferenceConfig":              map[string]any{"maxTokens": 99},
				"additionalModelRequestFields": map[string]any{"top_k": 5},
			},
			"other-provider": map[string]any{
				"inferenceConfig": map[string]any{"maxTokens": 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.lastRawBody, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}

	var ic map[string]json.RawMessage
	if err := json.Unmarshal(raw["inferenceConfig"], &ic); err != nil {
		t.Fatalf("decode inferenceConfig: %v", err)
	}
	var maxT float64
	if err := json.Unmarshal(ic["maxTokens"], &maxT); err != nil {
		t.Fatalf("decode maxTokens: %v", err)
	}
	if maxT != 99 {
		t.Errorf("inferenceConfig.maxTokens = %v, want provider option override 99", maxT)
	}

	amrfRaw, ok := raw["additionalModelRequestFields"]
	if !ok {
		t.Fatalf("request missing novel passthrough key additionalModelRequestFields: %s", fs.lastRawBody)
	}
	var amrf map[string]float64
	if err := json.Unmarshal(amrfRaw, &amrf); err != nil {
		t.Fatalf("decode additionalModelRequestFields: %v", err)
	}
	if amrf["top_k"] != 5 {
		t.Errorf("additionalModelRequestFields.top_k = %v, want 5", amrf["top_k"])
	}
}

func TestChatProviderOptionsOtherProviderKeyIgnored(t *testing.T) {
	model, fs := newTestModel(t)

	maxTokens := 7
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		MaxTokens: &maxTokens,
		ProviderOptions: map[string]any{
			"other-provider": map[string]any{
				"inferenceConfig": map[string]any{"maxTokens": 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(fs.lastRawBody, &raw); err != nil {
		t.Fatalf("decode raw request: %v", err)
	}
	var ic map[string]json.RawMessage
	if err := json.Unmarshal(raw["inferenceConfig"], &ic); err != nil {
		t.Fatalf("decode inferenceConfig: %v", err)
	}
	var maxT float64
	if err := json.Unmarshal(ic["maxTokens"], &maxT); err != nil {
		t.Fatalf("decode maxTokens: %v", err)
	}
	if maxT != 7 {
		t.Errorf("inferenceConfig.maxTokens = %v, want unchanged 7 (other provider's options must be ignored)", maxT)
	}
}
