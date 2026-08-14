package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// handRolledPanicTool is a Tool implementation that does NOT go through
// NewTool, so it doesn't benefit from (*tool).Execute's internal recover.
// Its Execute panics unconditionally, pinning the requirement that the
// loop-level guard in executeToolCall (generate_text.go) covers ANY Tool
// implementation, not just ones built with NewTool.
type handRolledPanicTool struct {
	name string
}

func (h *handRolledPanicTool) Name() string                     { return h.name }
func (h *handRolledPanicTool) Description() string              { return "" }
func (h *handRolledPanicTool) Schema() json.RawMessage          { return json.RawMessage(`{"type":"object"}`) }
func (h *handRolledPanicTool) Strict() bool                     { return false }
func (h *handRolledPanicTool) InputExamples() []json.RawMessage { return nil }
func (h *handRolledPanicTool) InputCallbacks() ToolInputCallbacks {
	return ToolInputCallbacks{}
}
func (h *handRolledPanicTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	panic("hand-rolled boom")
}

// handRolledApprovalPanicTool implements both Tool and ApprovalRequirer by
// hand; ApprovalRequired panics unconditionally.
type handRolledApprovalPanicTool struct {
	handRolledPanicTool
	executed *bool
}

func (h *handRolledApprovalPanicTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	if h.executed != nil {
		*h.executed = true
	}
	return "ok", nil
}

func (h *handRolledApprovalPanicTool) ApprovalRequired(ctx context.Context, args json.RawMessage) bool {
	panic("approval check boom")
}

// TestHandRolledToolExecutePanicRecovered pins the requirement that a
// caller-implemented (non-NewTool) Tool whose Execute panics is converted to
// a *ToolExecutionError at the loop level (executeToolCall), rather than
// crashing the process.
func TestHandRolledToolExecutePanicRecovered(t *testing.T) {
	tool := &handRolledPanicTool{name: "hand_rolled"}
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("hand_rolled", "c1", `{}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var te *ToolExecutionError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &te) {
		t.Fatalf("err = %v, want *ToolExecutionError", res.Steps[0].ToolResults[0].Err)
	}
	if te.ToolName != "hand_rolled" {
		t.Fatalf("ToolName = %q", te.ToolName)
	}
	if len(te.Stack) == 0 {
		t.Fatal("Stack is empty, want captured stack trace")
	}
}

// TestApprovalRequiredPanicRecoveredBatchMatesUnaffected pins the
// requirement that a panicking ApprovalRequirer.ApprovalRequired hook fails
// just that tool call (as a *ToolExecutionError, routed exactly as an
// Execute error) without affecting other calls in the same batch.
func TestApprovalRequiredPanicRecoveredBatchMatesUnaffected(t *testing.T) {
	var executed bool
	panicky := &handRolledApprovalPanicTool{handRolledPanicTool: handRolledPanicTool{name: "panicky"}}
	plain := NewTool("plain", "", func(_ context.Context, a weatherArgs) (any, error) {
		executed = true
		return "sunny", nil
	})

	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content: []provider.ContentPart{
				provider.ToolCallPart{ID: "c1", Name: "panicky", Args: []byte(`{}`)},
				provider.ToolCallPart{ID: "c2", Name: "plain", Args: []byte(`{"city":"Ghent"}`)},
			},
			FinishReason: provider.FinishToolCalls,
		},
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{panicky, plain}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps[0].ToolResults) != 2 {
		t.Fatalf("ToolResults = %+v, want 2 results", res.Steps[0].ToolResults)
	}

	var got1, got2 *ToolResultRecord
	for i := range res.Steps[0].ToolResults {
		r := &res.Steps[0].ToolResults[i]
		switch r.ToolCallID {
		case "c1":
			got1 = r
		case "c2":
			got2 = r
		}
	}
	if got1 == nil || got2 == nil {
		t.Fatalf("missing results: %+v", res.Steps[0].ToolResults)
	}

	var te *ToolExecutionError
	if !errors.As(got1.Err, &te) {
		t.Fatalf("c1 err = %v, want *ToolExecutionError", got1.Err)
	}
	if te.ToolName != "panicky" {
		t.Fatalf("ToolName = %q", te.ToolName)
	}
	if len(te.Stack) == 0 {
		t.Fatal("Stack is empty, want captured stack trace")
	}

	if got2.Err != nil {
		t.Fatalf("c2 err = %v, want nil (batch mate unaffected)", got2.Err)
	}
	if got2.Result != "sunny" {
		t.Fatalf("c2 result = %v, want sunny", got2.Result)
	}
	if !executed {
		t.Fatal("plain tool should have executed despite panicky's approval check panicking")
	}
}
