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

// TestReasoningExplicitBudget asserts call.Reasoning.BudgetTokens
// serializes as additionalModelRequestFields.thinking.budget_tokens
// verbatim when set explicitly.
func TestReasoningExplicitBudget(t *testing.T) {
	model, fs := newTestModel(t)

	budget := 2048
	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{Effort: "high", BudgetTokens: &budget},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	amrf := fs.lastRequest.AdditionalModelRequestFields
	thinking, ok := amrf["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("additionalModelRequestFields.thinking missing or wrong type: %#v", amrf)
	}
	if thinking["type"] != "enabled" {
		t.Errorf("thinking.type = %v, want enabled", thinking["type"])
	}
	if thinking["budget_tokens"] != float64(2048) {
		t.Errorf("thinking.budget_tokens = %v, want 2048 (explicit BudgetTokens wins over Effort)", thinking["budget_tokens"])
	}
}

// TestReasoningNoPreexistingProviderOptions asserts the Reasoning-derived
// additionalModelRequestFields.thinking is sent as-is when ProviderOptions
// sets no bedrock.additionalModelRequestFields entry at all.
func TestReasoningNoPreexistingProviderOptions(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{Effort: "medium"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	amrf := fs.lastRequest.AdditionalModelRequestFields
	thinking, ok := amrf["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("additionalModelRequestFields.thinking missing or wrong type: %#v", amrf)
	}
	if thinking["budget_tokens"] != float64(8192) {
		t.Errorf("thinking.budget_tokens = %v, want 8192 (medium)", thinking["budget_tokens"])
	}
}

// TestReasoningMergesWithPreexistingProviderOptionsAdditionalFields asserts
// that when ProviderOptions ALSO sets bedrock.additionalModelRequestFields
// (with a different sub-key), the Reasoning-derived "thinking" entry and
// the ProviderOptions entry both survive — a per-sub-key merge, not a
// wholesale replace.
func TestReasoningMergesWithPreexistingProviderOptionsAdditionalFields(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{Effort: "high"},
		ProviderOptions: map[string]any{
			"bedrock": map[string]any{
				"additionalModelRequestFields": map[string]any{"top_k": 5},
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
	var amrf map[string]json.RawMessage
	if err := json.Unmarshal(raw["additionalModelRequestFields"], &amrf); err != nil {
		t.Fatalf("decode additionalModelRequestFields: %v", err)
	}
	if _, ok := amrf["thinking"]; !ok {
		t.Errorf("additionalModelRequestFields missing thinking (Reasoning-derived entry lost to wholesale replace): %s", raw["additionalModelRequestFields"])
	}
	var topK float64
	if err := json.Unmarshal(amrf["top_k"], &topK); err != nil || topK != 5 {
		t.Errorf("additionalModelRequestFields.top_k = %s, want 5", amrf["top_k"])
	}
}

// TestReasoningProviderOptionsKeyWinsOnCollision asserts that when
// ProviderOptions sets bedrock.additionalModelRequestFields.thinking
// directly (same key the Reasoning mapping would populate), the
// ProviderOptions value wins.
func TestReasoningProviderOptionsKeyWinsOnCollision(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{Effort: "high"},
		ProviderOptions: map[string]any{
			"bedrock": map[string]any{
				"additionalModelRequestFields": map[string]any{
					"thinking": map[string]any{"type": "enabled", "budget_tokens": 1},
				},
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
	var amrf map[string]json.RawMessage
	if err := json.Unmarshal(raw["additionalModelRequestFields"], &amrf); err != nil {
		t.Fatalf("decode additionalModelRequestFields: %v", err)
	}
	var thinking map[string]any
	if err := json.Unmarshal(amrf["thinking"], &thinking); err != nil {
		t.Fatalf("decode thinking: %v", err)
	}
	if thinking["budget_tokens"] != float64(1) {
		t.Errorf("thinking.budget_tokens = %v, want 1 (ProviderOptions must win on key collision)", thinking["budget_tokens"])
	}
}

// TestReasoningNeitherResolvesOmitsThinking asserts that Reasoning set with
// neither BudgetTokens nor a recognized Effort omits
// additionalModelRequestFields.thinking entirely (and the field itself, if
// nothing else populates it).
func TestReasoningNeitherResolvesOmitsThinking(t *testing.T) {
	model, fs := newTestModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("simple")},
		Reasoning: &provider.ReasoningConfig{},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if fs.lastRequest.AdditionalModelRequestFields != nil {
		t.Errorf("AdditionalModelRequestFields = %#v, want nil", fs.lastRequest.AdditionalModelRequestFields)
	}
}
