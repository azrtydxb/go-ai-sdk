package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestAsToolSchema verifies AsTool's hand-written schema matches the spec
// exactly, with the sub-agent's name interpolated into the description.
func TestAsToolSchema(t *testing.T) {
	sub := &Agent{Model: &aitest.MockModel{}}
	tool := AsTool(sub, "researcher", "delegates research tasks")

	if tool.Name() != "researcher" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() != "delegates research tasks" {
		t.Fatalf("Description = %q", tool.Description())
	}

	want := `{"type":"object","properties":{"task":{"type":"string","description":"The task for the researcher agent."}},"required":["task"],"additionalProperties":false}`
	var gotJSON, wantJSON any
	if err := json.Unmarshal(tool.Schema(), &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatal(err)
	}
	gotBytes, _ := json.Marshal(gotJSON)
	wantBytes, _ := json.Marshal(wantJSON)
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("Schema = %s, want %s", tool.Schema(), want)
	}
}

// TestAsToolExecutesSubAgentAndReturnsText verifies AsTool runs a scripted
// sub-agent loop (including its own tool call) and returns the sub-agent's
// final text.
func TestAsToolExecutesSubAgentAndReturnsText(t *testing.T) {
	sub := &Agent{
		Model: &aitest.MockModel{Responses: []*provider.Response{
			toolCallResponse("t", "1", `{}`),
			textResponse("sub-agent final answer"),
		}},
		Tools: []ai.Tool{echoTool("t")},
	}
	tool := AsTool(sub, "helper", "a helper agent")

	res, err := tool.Execute(context.Background(), []byte(`{"task":"do the thing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res != "sub-agent final answer" {
		t.Fatalf("res = %v", res)
	}
}

type outAnswer struct {
	Answer string `json:"answer"`
}

// TestAsToolWithOutputReturnsDecodedValue verifies that when the sub-agent
// has an Output set, AsTool returns the decoded Output rather than Text.
func TestAsToolWithOutputReturnsDecodedValue(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{
			{
				Content:      []provider.ContentPart{provider.TextPart{Text: `{"answer":"42"}`}},
				FinishReason: provider.FinishStop,
			},
		},
	}
	sub := &Agent{
		Model:  m,
		Output: ai.OutputObject[outAnswer](),
	}
	tool := AsTool(sub, "helper", "a helper agent")

	res, err := tool.Execute(context.Background(), []byte(`{"task":"answer"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.(outAnswer)
	if !ok {
		t.Fatalf("res = %#v (%T), want outAnswer", res, res)
	}
	if got.Answer != "42" {
		t.Fatalf("Answer = %q", got.Answer)
	}
}

// TestAsToolSubAgentErrorPropagates verifies a sub-agent failure surfaces as
// the tool's execution error, wrapped in *ai.ToolExecutionError — matching
// the error taxonomy ai.NewTool-built tools already produce for a failing
// handler.
func TestAsToolSubAgentErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	sub := &Agent{Model: &aitest.MockModel{Err: wantErr}}
	tool := AsTool(sub, "helper", "a helper agent")

	_, err := tool.Execute(context.Background(), []byte(`{"task":"x"}`))
	if err == nil {
		t.Fatal("want error")
	}
	var tee *ai.ToolExecutionError
	if !errors.As(err, &tee) {
		t.Fatalf("err = %v, want *ai.ToolExecutionError", err)
	}
	if tee.ToolName != "helper" {
		t.Fatalf("ToolName = %q, want %q", tee.ToolName, "helper")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

// TestAsToolSubAgentErrorRecordedAsToolExecutionErrorInParentLoop verifies
// the same thing end-to-end: when a parent agent's tool loop runs the
// AsTool-wrapped sub-agent and it fails, the parent's
// ToolResultRecord.Err is a *ai.ToolExecutionError (not the sub-agent's raw
// error), exactly like any other failing tool built with ai.NewTool.
func TestAsToolSubAgentErrorRecordedAsToolExecutionErrorInParentLoop(t *testing.T) {
	wantErr := errors.New("boom")
	sub := &Agent{Model: &aitest.MockModel{Err: wantErr}}
	subTool := AsTool(sub, "helper", "a helper agent")

	parentModel := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("helper", "c1", `{"task":"x"}`),
		textResponse("done"),
	}}
	parent := &Agent{Model: parentModel, Tools: []ai.Tool{subTool}, MaxSteps: 2}

	res, err := parent.Generate(t.Context(), RunOpts{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	var tee *ai.ToolExecutionError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &tee) {
		t.Fatalf("err = %v, want *ai.ToolExecutionError", res.Steps[0].ToolResults[0].Err)
	}
	if tee.ToolName != "helper" {
		t.Fatalf("ToolName = %q, want %q", tee.ToolName, "helper")
	}
}

// TestAsToolInheritsParentRuntimeContextWhenSubAgentHasNone verifies the
// documented inheritance rule: a sub-agent with no RuntimeContext of its
// own sees whatever RuntimeContext the parent installed, because a nil
// RuntimeContext is a no-op for ai.GenerateTextOpts.RuntimeContext (it
// leaves ctx's existing installation untouched) rather than clearing it.
func TestAsToolInheritsParentRuntimeContextWhenSubAgentHasNone(t *testing.T) {
	parentRC := ai.RuntimeContext{"user": "parent-value"}
	var capturedRC ai.RuntimeContext

	checkTool := ai.NewTool("check", "", func(ctx context.Context, a noArgs) (any, error) {
		capturedRC = ai.RuntimeContextFrom(ctx)
		return "checked", nil
	})

	subModel := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("check", "s1", `{}`),
		textResponse("sub done"),
	}}
	sub := &Agent{Model: subModel, Tools: []ai.Tool{checkTool}, MaxSteps: 2}
	// sub.RuntimeContext intentionally left unset.
	subTool := AsTool(sub, "helper", "a helper agent")

	parentModel := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("helper", "p1", `{"task":"x"}`),
		textResponse("parent done"),
	}}
	parent := &Agent{
		Model:          parentModel,
		Tools:          []ai.Tool{subTool},
		MaxSteps:       2,
		RuntimeContext: parentRC,
	}

	if _, err := parent.Generate(t.Context(), RunOpts{Prompt: "go"}); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(capturedRC, parentRC) {
		t.Fatalf("capturedRC = %#v, want parent's %#v (inherited)", capturedRC, parentRC)
	}
}

// TestAsToolOwnRuntimeContextOverridesParent verifies the other half of the
// rule: when the sub-agent DOES set its own RuntimeContext, it overrides
// (shadows) whatever the parent installed, for the sub-agent's own tools.
func TestAsToolOwnRuntimeContextOverridesParent(t *testing.T) {
	parentRC := ai.RuntimeContext{"user": "parent-value"}
	subRC := ai.RuntimeContext{"user": "sub-value"}
	var capturedRC ai.RuntimeContext

	checkTool := ai.NewTool("check", "", func(ctx context.Context, a noArgs) (any, error) {
		capturedRC = ai.RuntimeContextFrom(ctx)
		return "checked", nil
	})

	subModel := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("check", "s1", `{}`),
		textResponse("sub done"),
	}}
	sub := &Agent{
		Model:          subModel,
		Tools:          []ai.Tool{checkTool},
		MaxSteps:       2,
		RuntimeContext: subRC,
	}
	subTool := AsTool(sub, "helper", "a helper agent")

	parentModel := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("helper", "p1", `{"task":"x"}`),
		textResponse("parent done"),
	}}
	parent := &Agent{
		Model:          parentModel,
		Tools:          []ai.Tool{subTool},
		MaxSteps:       2,
		RuntimeContext: parentRC,
	}

	if _, err := parent.Generate(t.Context(), RunOpts{Prompt: "go"}); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(capturedRC, subRC) {
		t.Fatalf("capturedRC = %#v, want sub's own %#v (override)", capturedRC, subRC)
	}
}

// TestAsToolBadArgsIsError verifies malformed args to the generated tool
// return an error rather than panicking.
func TestAsToolBadArgsIsError(t *testing.T) {
	sub := &Agent{Model: &aitest.MockModel{}}
	tool := AsTool(sub, "helper", "a helper agent")

	if _, err := tool.Execute(context.Background(), []byte(`not json`)); err == nil {
		t.Fatal("want error for malformed args")
	}
}
