package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func toolCallResponse(name, id, args string) *provider.Response {
	return &provider.Response{
		Content:      []provider.ContentPart{provider.ToolCallPart{ID: id, Name: name, Args: []byte(args)}},
		FinishReason: provider.FinishToolCalls,
	}
}

func textResponse(text string) *provider.Response {
	return &provider.Response{
		Content:      []provider.ContentPart{provider.TextPart{Text: text}},
		FinishReason: provider.FinishStop,
	}
}

type noArgs struct{}

func echoTool(name string) ai.Tool {
	return ai.NewTool(name, "", func(ctx context.Context, a noArgs) (any, error) {
		return "ok", nil
	})
}

// TestGenerateAssemblesOpts verifies Generate builds the GenerateTextOpts
// from the Agent's fields: system message from Instructions, tools present,
// and a MaxSteps large enough to let a scripted multi-step loop finish.
func TestGenerateAssemblesOpts(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "1", `{}`),
		toolCallResponse("t", "2", `{}`),
		textResponse("done"),
	}}
	a := &Agent{
		Model:        m,
		Instructions: "be helpful",
		Tools:        []ai.Tool{echoTool("t")},
		MaxSteps:     5,
	}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q", res.Text)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("Steps = %d, want 3", len(res.Steps))
	}

	call := m.Calls[0]
	if len(call.Messages) == 0 || call.Messages[0].Role != provider.RoleSystem {
		t.Fatal("system message not first")
	}
	if len(call.Tools) != 1 || call.Tools[0].Name != "t" {
		t.Fatalf("Tools = %+v", call.Tools)
	}
}

// TestGenerateDefaultMaxSteps verifies MaxSteps defaults to 8 (not ai's own
// default of 1) when Agent.MaxSteps is 0, by scripting a loop that requires
// more than 1 step but fewer than 8.
func TestGenerateDefaultMaxSteps(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "1", `{}`),
		toolCallResponse("t", "2", `{}`),
		toolCallResponse("t", "3", `{}`),
		textResponse("done"),
	}}
	a := &Agent{
		Model: m,
		Tools: []ai.Tool{echoTool("t")},
	}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q, want done (default MaxSteps should allow 4 steps)", res.Text)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("Steps = %d, want 4", len(res.Steps))
	}
}

// TestGenerateDefaultMaxStepsCapsAtEight verifies the default of 8 is
// actually enforced as a cap (not effectively unlimited) by scripting a
// loop that would run forever without a cap.
func TestGenerateDefaultMaxStepsCapsAtEight(t *testing.T) {
	var responses []*provider.Response
	for i := 0; i < 20; i++ {
		responses = append(responses, toolCallResponse("t", "x", `{}`))
	}
	m := &aitest.MockModel{Responses: responses}
	a := &Agent{
		Model: m,
		Tools: []ai.Tool{echoTool("t")},
	}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 8 {
		t.Fatalf("Steps = %d, want 8 (default MaxSteps cap)", len(res.Steps))
	}
}

// TestPrepareOptsRunsLastAndWins verifies PrepareOpts is applied after every
// other Agent field, and that whatever it sets on opts is what actually
// gets used.
func TestPrepareOptsRunsLastAndWins(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{textResponse("done")}}
	a := &Agent{
		Model:        m,
		Instructions: "original",
		MaxSteps:     2,
		PrepareOpts: func(opts *ai.GenerateTextOpts) {
			opts.System = "overridden"
			opts.MaxSteps = 1
		},
	}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" {
		t.Fatal("expected result")
	}
	call := m.Calls[0]
	if call.Messages[0].Role != provider.RoleSystem {
		t.Fatal("system message not first")
	}
	tp, ok := call.Messages[0].Content[0].(provider.TextPart)
	if !ok || tp.Text != "overridden" {
		t.Fatalf("system content = %+v, want overridden", call.Messages[0].Content)
	}
}

// TestStreamSmoke verifies Stream delegates to ai.StreamText and returns a
// usable *ai.TextStream.
func TestStreamSmoke(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.TextDelta{Text: "hi"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	a := &Agent{Model: m}
	stream, err := a.Stream(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for part := range stream.Parts() {
		if td, ok := part.(provider.TextDelta); ok {
			got += td.Text
		}
	}
	if stream.Err() != nil {
		t.Fatal(stream.Err())
	}
	if got != "hi" {
		t.Fatalf("got = %q", got)
	}
}

// TestStreamWithOutputPassesThroughError verifies Stream does not intercept
// or drop ai.ErrOutputWithStreamText when Output is set — it's passed
// through unchanged, same as calling ai.StreamText directly.
func TestStreamWithOutputPassesThroughError(t *testing.T) {
	a := &Agent{
		Model:  &aitest.MockModel{},
		Output: ai.OutputObject[struct{ X string }](),
	}
	_, err := a.Stream(t.Context(), RunOpts{Prompt: "hi"})
	if !errors.Is(err, ai.ErrOutputWithStreamText) {
		t.Fatalf("err = %v, want ai.ErrOutputWithStreamText", err)
	}
}

// TestApproveToolCallDenialVisibleInTranscript verifies Agent.ApproveToolCall
// is passed through: a denial shows up in the transcript exactly like it
// would via raw ai.GenerateText.
func TestApproveToolCallDenialVisibleInTranscript(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{}`),
		textResponse("done"),
	}}
	a := &Agent{
		Model: m,
		Tools: []ai.Tool{ai.RequireApproval(echoTool("t"))},
		ApproveToolCall: func(_ context.Context, req ai.ApprovalRequest) (ai.ApprovalDecision, bool) {
			return ai.ApprovalDecision{ToolCallID: req.Call.ID, Approved: false, Reason: "denied by policy"}, true
		},
	}
	res, err := a.Generate(t.Context(), RunOpts{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var denied *ai.ToolApprovalDeniedError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &denied) {
		t.Fatalf("err = %v, want *ai.ToolApprovalDeniedError", res.Steps[0].ToolResults[0].Err)
	}
	if denied.Reason != "denied by policy" {
		t.Fatalf("Reason = %q", denied.Reason)
	}
}

// TestRunOptsValidationDelegatesToAI verifies both-or-neither Prompt/
// Messages is rejected with the same error ai.GenerateText itself returns —
// Agent does not duplicate this validation.
func TestRunOptsValidationDelegatesToAI(t *testing.T) {
	a := &Agent{Model: &aitest.MockModel{}}

	if _, err := a.Generate(t.Context(), RunOpts{}); err == nil {
		t.Fatal("want error when neither Prompt nor Messages set")
	}
	if _, err := a.Generate(t.Context(), RunOpts{
		Prompt:   "x",
		Messages: []provider.Message{provider.UserText("y")},
	}); err == nil {
		t.Fatal("want error when both Prompt and Messages set")
	}
}

func TestGenerateNilModel(t *testing.T) {
	a := &Agent{}
	if _, err := a.Generate(t.Context(), RunOpts{Prompt: "x"}); err == nil {
		t.Fatal("want error when Model is nil")
	}
}
