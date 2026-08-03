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
	_, err := runToolCalls(t.Context(), []Tool{known}, calls, nil, nil)
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
		PrepareStep: func(stepIndex int, plan StepPlan) (StepPlan, bool) {
			if stepIndex == 1 {
				plan.Call.ToolChoice = required
				return plan, true
			}
			return StepPlan{}, false
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

// TestPrepareStepSwapsModelAndPersists covers StepPlan.Model: PrepareStep
// swaps to model B on step 1 (leaving it unset, i.e. nil, on every other
// step), and the swap must persist — model B, not the original model A,
// must also make the call for step 2, even though PrepareStep didn't set
// Model again on that step.
func TestPrepareStepSwapsModelAndPersists(t *testing.T) {
	modelA := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
	}}
	modelB := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c2", `{"city":"b"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })

	var sawModelOnStep1 provider.LanguageModel
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: modelA, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
		PrepareStep: func(stepIndex int, plan StepPlan) (StepPlan, bool) {
			if stepIndex == 1 {
				sawModelOnStep1 = plan.Model
				plan.Model = modelB
				return plan, true
			}
			return StepPlan{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sawModelOnStep1 != modelA {
		t.Fatalf("plan.Model seen at step 1 = %v, want modelA (the model active before the swap)", sawModelOnStep1)
	}
	if len(modelA.Calls) != 1 {
		t.Fatalf("modelA.Calls = %d, want 1 (only step 0)", len(modelA.Calls))
	}
	if len(modelB.Calls) != 2 {
		t.Fatalf("modelB.Calls = %d, want 2 (steps 1 and 2 — the swap must persist)", len(modelB.Calls))
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q, want %q", res.Text, "done")
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

// TestActiveToolsFiltersOfferedToolDefs verifies ActiveTools limits which
// tools are OFFERED (ToolDefs in the Call) while a call to a still-active
// tool executes normally against the full Tools implementation.
func TestActiveToolsFiltersOfferedToolDefs(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sunny"}},
			FinishReason: provider.FinishStop},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	other := NewTool("get_time", "", func(_ context.Context, a weatherArgs) (any, error) { return "noon", nil })
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather, other},
		ActiveTools: []string{"get_weather"}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The offered ToolDefs on the first call must include only get_weather.
	if len(m.Calls[0].Tools) != 1 || m.Calls[0].Tools[0].Name != "get_weather" {
		t.Fatalf("offered tools = %+v, want only get_weather", m.Calls[0].Tools)
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("active tool execution failed: %+v", res.Steps[0].ToolResults)
	}
}

// TestActiveToolsInactiveCallIsNoSuchTool verifies that calling a tool
// outside the active set is treated as unknown (NoSuchToolError), even
// though it's present in Tools.
func TestActiveToolsInactiveCallIsNoSuchTool(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_time", "c1", `{}`),
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	other := NewTool("get_time", "", func(_ context.Context, a weatherArgs) (any, error) { return "noon", nil })
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather, other},
		ActiveTools: []string{"get_weather"},
	})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "get_time" {
		t.Fatalf("err = %v, want NoSuchToolError(get_time)", err)
	}
}

// TestActiveToolsEmptySliceOffersNoToolsAndRejectsAnyCall verifies the
// documented distinction between a nil and an empty (but non-nil)
// ActiveTools: []string{} is not "no filtering" — it replaces the active
// set with the empty set, so zero ToolDefs are offered to the model and any
// tool call the model makes anyway (Tools is non-empty; the model just
// isn't supposed to call any of it) is treated as unknown.
func TestActiveToolsEmptySliceOffersNoToolsAndRejectsAnyCall(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather},
		ActiveTools: []string{},
	})
	if len(m.Calls[0].Tools) != 0 {
		t.Fatalf("offered tools = %+v, want none", m.Calls[0].Tools)
	}
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "get_weather" {
		t.Fatalf("err = %v, want NoSuchToolError(get_weather)", err)
	}
}

// TestRepairToolCallFixesUnknownName verifies that a hallucinated tool name
// ("get_wether") is corrected by RepairToolCall to a known one
// ("get_weather"), letting the loop proceed normally.
func TestRepairToolCallFixesUnknownName(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_wether", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sunny"}},
			FinishReason: provider.FinishStop},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			var nst *NoSuchToolError
			if errors.As(toolErr, &nst) && call.Name == "get_wether" {
				call.Name = "get_weather"
				return call, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("repaired call did not execute: %+v", res.Steps[0].ToolResults)
	}
}

// TestRepairToolCallFixesBadArgs verifies that an InvalidToolArgumentsError
// from Execute is offered to RepairToolCall, and the corrected args are
// re-executed.
func TestRepairToolCallFixesBadArgs(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"bogus":1}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sunny"}},
			FinishReason: provider.FinishStop},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny in " + a.City, nil })
	var repairCalls int
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			var iae *InvalidToolArgumentsError
			if errors.As(toolErr, &iae) {
				call.Args = []byte(`{"city":"Ghent"}`)
				return call, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
	if res.Steps[0].ToolResults[0].Err != nil {
		t.Fatalf("tool result err = %v, want nil after repair", res.Steps[0].ToolResults[0].Err)
	}
	if res.Steps[0].ToolResults[0].Result != "sunny in Ghent" {
		t.Fatalf("result = %v", res.Steps[0].ToolResults[0].Result)
	}
}

// TestRepairToolCallFalseKeepsOriginalError verifies that RepairToolCall
// returning false leaves the original error semantics in place: a
// NoSuchToolError still aborts the loop.
func TestRepairToolCallFalseKeepsOriginalError(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("nope", "c1", `{}`),
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather},
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			return ToolCallRecord{}, false
		},
	})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "nope" {
		t.Fatalf("err = %v, want NoSuchToolError(nope)", err)
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
}

// TestRepairToolCallSingleShotCap verifies that a repaired call which fails
// again does NOT re-invoke RepairToolCall a second time for the same
// original call.
func TestRepairToolCallSingleShotCap(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("nope", "c1", `{}`),
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather},
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			// "Correct" to another, still-unknown name — the repaired call
			// must fail validation again, and RepairToolCall must not be
			// invoked a second time for it.
			call.Name = "still_unknown"
			return call, true
		},
	})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "still_unknown" {
		t.Fatalf("err = %v, want NoSuchToolError(still_unknown)", err)
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1 (no re-invocation on second failure)", repairCalls)
	}
}

// TestRepairToolCallSingleShotCapOnBadArgs verifies the same single-shot cap
// for the bad-args (Execute) path: a repaired call whose args are still
// invalid does not trigger a second RepairToolCall invocation, and the
// second failure is recorded normally rather than aborting the loop.
func TestRepairToolCallSingleShotCapOnBadArgs(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"bogus":1}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sorry"}},
			FinishReason: provider.FinishStop},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			// Still-invalid args: the retried Execute fails again.
			call.Args = []byte(`{"also_bogus":2}`)
			return call, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1 (no re-invocation on second failure)", repairCalls)
	}
	if res.Steps[0].ToolResults[0].Err == nil {
		t.Fatal("expected recorded tool error after second (unrepaired) failure")
	}
}

// ---------------------------------------------------------------------
// HasToolCall / LoopFinished / StopWhen-every-step
// ---------------------------------------------------------------------

func TestHasToolCallAnyName(t *testing.T) {
	f := HasToolCall()
	if f(nil) {
		t.Fatal("empty steps should be false")
	}
	if f([]Step{{}}) {
		t.Fatal("step with no tool calls should be false")
	}
	if !f([]Step{{ToolCalls: []ToolCallRecord{{Name: "x"}}}}) {
		t.Fatal("step with any tool call should be true")
	}
}

func TestHasToolCallNamedOnlyLastStep(t *testing.T) {
	f := HasToolCall("get_weather")
	steps := []Step{
		{ToolCalls: []ToolCallRecord{{Name: "get_weather"}}}, // earlier step: irrelevant
		{ToolCalls: []ToolCallRecord{{Name: "other"}}},       // last step: not the named tool
	}
	if f(steps) {
		t.Fatal("should only consult the LAST step")
	}
	steps[len(steps)-1] = Step{ToolCalls: []ToolCallRecord{{Name: "other"}, {Name: "get_weather"}}}
	if !f(steps) {
		t.Fatal("last step calling the named tool (among others) should be true")
	}
}

func TestLoopFinished(t *testing.T) {
	f := LoopFinished()
	if f(nil) {
		t.Fatal("empty steps should be false")
	}
	if f([]Step{{ToolCalls: []ToolCallRecord{{Name: "t"}}}}) {
		t.Fatal("last step with tool calls should be false")
	}
	if !f([]Step{{ToolCalls: []ToolCallRecord{{Name: "t"}}}, {}}) {
		t.Fatal("last step with no tool calls should be true")
	}
}

// TestGenerateTextStopWhenConsultedOnEveryStep verifies the StopWhen
// consultation-rule change: StopWhen is now called after EVERY step,
// including one with no tool calls (which previously ended the loop before
// StopWhen was ever consulted for it). The natural end (no tool calls)
// still stops the loop unconditionally either way, so this is observed via
// a side-effecting StopWhen closure rather than a difference in Steps
// count.
func TestGenerateTextStopWhenConsultedOnEveryStep(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	var calls int
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 5,
		StopWhen: func(steps []Step) bool {
			calls++
			return false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if calls != 2 {
		t.Fatalf("StopWhen calls = %d, want 2 (consulted on every step, incl. the final no-tool-call step)", calls)
	}
}

func TestStreamTextStopWhenConsultedOnEveryStep(t *testing.T) {
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
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	var calls int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 5,
		StopWhen: func(steps []Step) bool {
			calls++
			return false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(s.Steps()) != 2 {
		t.Fatalf("steps = %d, want 2", len(s.Steps()))
	}
	if calls != 2 {
		t.Fatalf("StopWhen calls = %d, want 2 (consulted on every step, incl. the final no-tool-call step)", calls)
	}
}

// TestGenerateTextLoopFinishedComposedWithStepCount verifies a realistic
// composition: stop at 10 steps OR when the loop would finish naturally,
// whichever comes first. Since the model here never stops calling tools on
// its own, only the step-count half fires — but LoopFinished must still be
// consulted (and return false) on every one of those steps without panicking
// or otherwise interfering.
func TestGenerateTextLoopFinishedComposedWithStepCount(t *testing.T) {
	responses := make([]*provider.Response, 5)
	for i := range responses {
		responses[i] = toolCallResponse("t", "c", `{"city":"x"}`)
	}
	m := &aitest.MockModel{Responses: responses}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	stepCount := StepCountIs(3)
	loopFinished := LoopFinished()
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 10,
		StopWhen: func(steps []Step) bool {
			return stepCount(steps) || loopFinished(steps)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(res.Steps))
	}
}
