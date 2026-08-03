package ai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestRuntimeContextFromReturnsNilWhenUnset(t *testing.T) {
	if rc := RuntimeContextFrom(t.Context()); rc != nil {
		t.Fatalf("RuntimeContextFrom = %v, want nil", rc)
	}
}

// TestRuntimeContextFromInsideToolExecute verifies RuntimeContext is
// installed on the ctx passed to Tool.Execute, in both GenerateText and
// StreamText.
func TestRuntimeContextFromInsideToolExecute(t *testing.T) {
	rc := RuntimeContext{"db": "conn"}
	var seen RuntimeContext
	tool := NewTool("t", "", func(ctx context.Context, a weatherArgs) (any, error) {
		seen = RuntimeContextFrom(ctx)
		return "r", nil
	})

	t.Run("GenerateText", func(t *testing.T) {
		seen = nil
		m := &aitest.MockModel{Responses: []*provider.Response{
			toolCallResponse("t", "c1", `{"city":"a"}`),
			{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
		}}
		_, err := GenerateText(t.Context(), GenerateTextOpts{
			Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2, RuntimeContext: rc,
		})
		if err != nil {
			t.Fatal(err)
		}
		if seen["db"] != "conn" {
			t.Fatalf("seen = %v", seen)
		}
	})

	t.Run("StreamText", func(t *testing.T) {
		seen = nil
		m := &aitest.MockModel{Streams: [][]provider.StreamPart{
			{
				provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"a"}`)}},
				provider.FinishPart{Reason: provider.FinishToolCalls},
			},
			{
				provider.TextDelta{Text: "done"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		}}
		s, err := StreamText(t.Context(), GenerateTextOpts{
			Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2, RuntimeContext: rc,
		})
		if err != nil {
			t.Fatal(err)
		}
		for range s.Parts() {
		}
		if s.Err() != nil {
			t.Fatal(s.Err())
		}
		if seen["db"] != "conn" {
			t.Fatalf("seen = %v", seen)
		}
	})
}

// TestRuntimeContextFromInsideApprovalCallbacks verifies RuntimeContext is
// installed on the ctx passed to ApprovalRequirer.ApprovalRequired and to
// ApproveToolCall.
func TestRuntimeContextFromInsideApprovalCallbacks(t *testing.T) {
	rc := RuntimeContext{"user": "alice"}
	var seenRequired, seenApprove RuntimeContext

	tool := RequireApproval(
		NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil }),
		func(ctx context.Context, args json.RawMessage) bool {
			seenRequired = RuntimeContextFrom(ctx)
			return true
		},
	)

	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2, RuntimeContext: rc,
		ApproveToolCall: func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			seenApprove = RuntimeContextFrom(ctx)
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: true}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenRequired["user"] != "alice" {
		t.Fatalf("ApprovalRequired's ctx RuntimeContext = %v", seenRequired)
	}
	if seenApprove["user"] != "alice" {
		t.Fatalf("ApproveToolCall's ctx RuntimeContext = %v", seenApprove)
	}
}
