package ai

import (
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestBuildCallThreadsTopKPenaltiesSeedHeaders asserts buildCall threads
// GenerateTextOpts' TopK, PresencePenalty, FrequencyPenalty, Seed, and
// Headers unchanged into the provider.Call it builds.
func TestBuildCallThreadsTopKPenaltiesSeedHeaders(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "hi"}},
		FinishReason: provider.FinishStop,
	}}}

	topK := 40
	presence := 0.5
	frequency := -0.5
	seed := int64(7)
	headers := map[string]string{"X-Custom-Header": "custom-value"}

	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:            m,
		Prompt:           "hi",
		TopK:             &topK,
		PresencePenalty:  &presence,
		FrequencyPenalty: &frequency,
		Seed:             &seed,
		Headers:          headers,
	})
	if err != nil {
		t.Fatal(err)
	}

	call := m.Calls[0]
	if call.TopK == nil || *call.TopK != 40 {
		t.Errorf("call.TopK = %v, want 40", call.TopK)
	}
	if call.PresencePenalty == nil || *call.PresencePenalty != 0.5 {
		t.Errorf("call.PresencePenalty = %v, want 0.5", call.PresencePenalty)
	}
	if call.FrequencyPenalty == nil || *call.FrequencyPenalty != -0.5 {
		t.Errorf("call.FrequencyPenalty = %v, want -0.5", call.FrequencyPenalty)
	}
	if call.Seed == nil || *call.Seed != 7 {
		t.Errorf("call.Seed = %v, want 7", call.Seed)
	}
	if call.Headers["X-Custom-Header"] != "custom-value" {
		t.Errorf("call.Headers = %v, want X-Custom-Header=custom-value", call.Headers)
	}
}

// TestBuildCallThreadsReasoning asserts buildCall threads
// GenerateTextOpts.Reasoning unchanged into the provider.Call it builds.
func TestBuildCallThreadsReasoning(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "hi"}},
		FinishReason: provider.FinishStop,
	}}}

	budget := 4096
	reasoning := &provider.ReasoningConfig{Effort: "high", BudgetTokens: &budget}

	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:     m,
		Prompt:    "hi",
		Reasoning: reasoning,
	})
	if err != nil {
		t.Fatal(err)
	}

	call := m.Calls[0]
	if call.Reasoning != reasoning {
		t.Errorf("call.Reasoning = %v, want the same *ReasoningConfig passed in (%v)", call.Reasoning, reasoning)
	}
}
