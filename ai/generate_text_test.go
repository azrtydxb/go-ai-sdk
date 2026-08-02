package ai

import (
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
