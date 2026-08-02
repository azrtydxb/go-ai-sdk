package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func toolCallResponse(name, id, args string) *provider.Response {
	return &provider.Response{
		Content: []provider.ContentPart{provider.ToolCallPart{
			ID: id, Name: name, Args: []byte(args)}},
		FinishReason: provider.FinishToolCalls,
		Usage:        provider.Usage{TotalTokens: 10},
	}
}

func TestToolLoopExecutesAndContinues(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "It is sunny."}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
	}}
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "sunny", nil
	})
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "It is sunny." {
		t.Fatalf("Text = %q", res.Text)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatal("tool result missing")
	}
	if res.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", res.Usage)
	}

	// second call must include assistant tool-call msg + tool result msg
	second := m.Calls[1].Messages
	if second[len(second)-1].Role != provider.RoleTool {
		t.Fatal("tool msg not appended")
	}
}

func TestToolLoopStopsAtMaxSteps(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		toolCallResponse("t", "c2", `{"city":"b"}`),
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if res.FinishReason != provider.FinishToolCalls {
		t.Fatalf("finish = %v", res.FinishReason)
	}
}

func TestToolLoopUnknownTool(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("nope", "c1", `{}`)}}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x",
		Tools: []Tool{NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil })}})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunToolCallsValidatesBeforeExecuting(t *testing.T) {
	invoked := false
	known := NewTool("known", "", func(_ context.Context, a weatherArgs) (any, error) {
		invoked = true
		return "should not run", nil
	})
	calls := []provider.ToolCallPart{
		{ID: "c1", Name: "known", Args: []byte(`{"city":"x"}`)},
		{ID: "c2", Name: "unknown", Args: []byte(`{}`)},
	}
	_, err := runToolCalls(t.Context(), []Tool{known}, calls)
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "unknown" {
		t.Fatalf("err = %v", err)
	}
	if invoked {
		t.Fatal("known tool's fn was invoked despite unknown tool later in the batch")
	}
}

func TestToolLoopRecordsToolErrorAndContinues(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"x"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sorry"}},
			FinishReason: provider.FinishStop},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) {
		return nil, errors.New("db down")
	})
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].ToolResults[0].Err == nil {
		t.Fatal("tool error not recorded")
	}
	// and the tool message sent to the model marks IsError
	msgs := m.Calls[1].Messages
	tr := msgs[len(msgs)-1].Content[0].(provider.ToolResultPart)
	if !tr.IsError {
		t.Fatal("IsError not set on tool result part")
	}
}

func TestStopWhenStopsBeforeMaxSteps(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		toolCallResponse("t", "c2", `{"city":"b"}`),
		toolCallResponse("t", "c3", `{"city":"c"}`),
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool},
		MaxSteps: 10,
		StopWhen: StepCountIs(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (StopWhen should stop before MaxSteps=10)", len(res.Steps))
	}
}

func TestStopWhenDefaultCapIs16(t *testing.T) {
	responses := make([]*provider.Response, 20)
	for i := range responses {
		responses[i] = toolCallResponse("t", "c", `{"city":"x"}`)
	}
	m := &aitest.MockModel{Responses: responses}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool},
		// MaxSteps unset: StopWhen never fires (always false), so the
		// default hard cap of 16 must kick in.
		StopWhen: func(steps []Step) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 16 {
		t.Fatalf("steps = %d, want 16 (default hard cap with StopWhen set)", len(res.Steps))
	}
}

func TestPrepareStepSwapsToolChoiceOnStep2(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		toolCallResponse("t", "c2", `{"city":"b"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	required := &provider.ToolChoice{Mode: provider.ToolChoiceRequired}
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
		PrepareStep: func(stepIndex int, call provider.Call) (provider.Call, bool) {
			if stepIndex == 1 {
				call.ToolChoice = required
				return call, true
			}
			return provider.Call{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(m.Calls))
	}
	if m.Calls[0].ToolChoice != nil {
		t.Fatalf("step 0 ToolChoice = %+v, want nil (unchanged)", m.Calls[0].ToolChoice)
	}
	if m.Calls[1].ToolChoice != required {
		t.Fatalf("step 1 ToolChoice = %+v, want %+v", m.Calls[1].ToolChoice, required)
	}
	if m.Calls[2].ToolChoice != nil {
		t.Fatalf("step 2 ToolChoice = %+v, want nil (unchanged)", m.Calls[2].ToolChoice)
	}
}

func TestOnStepFinishInvokedOncePerStep(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 3}},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	var finished []Step
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
		OnStepFinish: func(step Step) {
			finished = append(finished, step)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(finished) != 2 {
		t.Fatalf("OnStepFinish calls = %d, want 2 (one per step, incl. final)", len(finished))
	}
	if finished[0].ToolCalls[0].Name != "t" {
		t.Fatalf("step 0 ToolCalls = %+v", finished[0].ToolCalls)
	}
	if finished[1].Text != "done" {
		t.Fatalf("step 1 (final) Text = %q, want %q", finished[1].Text, "done")
	}
	if finished[1].FinishReason != provider.FinishStop {
		t.Fatalf("step 1 FinishReason = %v", finished[1].FinishReason)
	}
	if len(finished) != len(res.Steps) {
		t.Fatalf("OnStepFinish count %d != len(res.Steps) %d", len(finished), len(res.Steps))
	}
}
