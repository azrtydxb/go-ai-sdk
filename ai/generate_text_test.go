package ai

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestMain(m *testing.M) {
	retry.BaseDelay = time.Millisecond
	m.Run()
}

func TestGenerateTextSimplePrompt(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "hello"}},
		FinishReason: provider.FinishStop,
		Usage:        provider.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4},
	}}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, System: "be brief", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hello" {
		t.Fatalf("Text = %q", res.Text)
	}
	if res.Usage.TotalTokens != 4 {
		t.Fatalf("Usage = %+v", res.Usage)
	}

	call := m.Calls[0]
	if call.Messages[0].Role != provider.RoleSystem {
		t.Fatal("system message not first")
	}
	if call.Messages[1].Role != provider.RoleUser {
		t.Fatal("user message missing")
	}
}

func TestGenerateTextReasoningText(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content: []provider.ContentPart{
			provider.ReasoningPart{Text: "let me think... "},
			provider.TextPart{Text: "hello"},
			provider.ReasoningPart{Text: "done."},
		},
		FinishReason: provider.FinishStop,
		Usage:        provider.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4},
	}}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReasoningText != "let me think... done." {
		t.Fatalf("ReasoningText = %q, want %q", res.ReasoningText, "let me think... done.")
	}
	if res.Text != "hello" {
		t.Fatalf("Text = %q, want %q (reasoning must not leak)", res.Text, "hello")
	}
	if res.Steps[0].ReasoningText != "let me think... done." {
		t.Fatalf("Steps[0].ReasoningText = %q", res.Steps[0].ReasoningText)
	}
}

// TestGenerateTextReasoningTextSkipsRedacted covers the case where a
// response mixes a visible ReasoningPart with a Redacted one (opaque
// provider-encrypted data): the redacted part's Text must not appear in
// Step.ReasoningText / GenerateTextResult.ReasoningText, but must remain in
// Response.Content for round-tripping.
func TestGenerateTextReasoningTextSkipsRedacted(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content: []provider.ContentPart{
			provider.ReasoningPart{Text: "visible"},
			provider.ReasoningPart{Redacted: true, Text: "CIPHERTEXT"},
			provider.TextPart{Text: "answer"},
		},
		FinishReason: provider.FinishStop,
	}}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReasoningText != "visible" {
		t.Fatalf("ReasoningText = %q, want %q (redacted text must be excluded)", res.ReasoningText, "visible")
	}
	if res.Steps[0].ReasoningText != "visible" {
		t.Fatalf("Steps[0].ReasoningText = %q, want %q", res.Steps[0].ReasoningText, "visible")
	}
	// The redacted part must still be present in the step's Response
	// content (needed to round-trip on a later turn).
	var haveRedacted bool
	for _, part := range res.Steps[0].Response.Content {
		if rp, ok := part.(provider.ReasoningPart); ok && rp.Redacted {
			haveRedacted = true
		}
	}
	if !haveRedacted {
		t.Fatal("redacted ReasoningPart missing from Steps[0].Response.Content")
	}
}

func TestGenerateTextUsageDetailsSummedAcrossSteps(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content:      []provider.ContentPart{provider.ToolCallPart{ID: "1", Name: "t", Args: []byte(`{}`)}},
			FinishReason: provider.FinishToolCalls,
			Usage:        provider.Usage{InputTokens: 5, OutputTokens: 2, TotalTokens: 7, CachedInputTokens: 1, ReasoningTokens: 1},
		},
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop,
			Usage:        provider.Usage{InputTokens: 6, OutputTokens: 3, TotalTokens: 9, CachedInputTokens: 2, ReasoningTokens: 2},
		},
	}}
	type args struct{}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "hi", MaxSteps: 2,
		Tools: []Tool{NewTool("t", "", func(ctx context.Context, a args) (any, error) { return "ok", nil })},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.CachedInputTokens != 3 || res.Usage.ReasoningTokens != 3 {
		t.Fatalf("Usage = %+v, want CachedInputTokens=3 ReasoningTokens=3", res.Usage)
	}
}

func TestGenerateTextValidation(t *testing.T) {
	if _, err := GenerateText(t.Context(), GenerateTextOpts{Model: &aitest.MockModel{}}); err == nil {
		t.Fatal("want error when neither Prompt nor Messages set")
	}
	if _, err := GenerateText(t.Context(), GenerateTextOpts{}); err == nil {
		t.Fatal("want error when Model nil")
	}
}

func TestGenerateTextRetriesThenWrapsError(t *testing.T) {
	m := &aitest.MockModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}
