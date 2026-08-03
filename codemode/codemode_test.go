package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// fakeSandbox is a Sandbox test double that records the code and Env it
// was invoked with, and drives its callback (if any) to exercise Env's
// CallTool binding before returning a scripted Result/error.
type fakeSandbox struct {
	gotCode  string
	gotEnv   Env
	callback func(ctx context.Context, env Env) (*Result, error)
	result   *Result
	err      error
}

func (s *fakeSandbox) Execute(ctx context.Context, code string, env Env) (*Result, error) {
	s.gotCode = code
	s.gotEnv = env
	if s.callback != nil {
		return s.callback(ctx, env)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// echoArgs is the argument shape for the "echo" test tool.
type echoArgs struct {
	Msg string `json:"msg"`
}

func newEchoTool() ai.Tool {
	return ai.NewTool("echo", "Echoes msg back.", func(_ context.Context, a echoArgs) (any, error) {
		return "echoed: " + a.Msg, nil
	})
}

func TestToolDescriptionContainsAPIDocAndLanguage(t *testing.T) {
	tool := Tool(&fakeSandbox{}, []ai.Tool{newEchoTool()}, &Options{Language: "python"})

	desc := tool.Description()
	if !strings.Contains(desc, "python") {
		t.Fatalf("description missing language: %q", desc)
	}
	if !strings.Contains(desc, APIDoc("python", []ai.Tool{newEchoTool()})) {
		t.Fatalf("description missing API doc: %q", desc)
	}
	if tool.Name() != "run_code" {
		t.Fatalf("Name() = %q, want run_code", tool.Name())
	}
}

func TestToolSchema(t *testing.T) {
	tool := Tool(&fakeSandbox{}, nil, nil)
	want := `{"type":"object","properties":{"code":{"type":"string","description":"The javascript code to execute."}},"required":["code"],"additionalProperties":false}`
	if string(tool.Schema()) != want {
		t.Fatalf("Schema() = %s, want %s", tool.Schema(), want)
	}
}

func TestDispatchRoutesToRightToolAndReturnsResult(t *testing.T) {
	sb := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			res, err := env.CallTool(ctx, "echo", json.RawMessage(`{"msg":"hi"}`))
			if err != nil {
				return nil, err
			}
			return &Result{Output: fmt.Sprintf("%v", res)}, nil
		},
	}
	tool := Tool(sb, []ai.Tool{newEchoTool()}, nil)

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"echo({msg: 'hi'})"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "echoed: hi" {
		t.Fatalf("got %q, want %q", got, "echoed: hi")
	}
	if sb.gotCode != "echo({msg: 'hi'})" {
		t.Fatalf("sandbox got code %q", sb.gotCode)
	}
}

func TestDispatchUnknownToolListsAvailableNames(t *testing.T) {
	sb := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			_, err := env.CallTool(ctx, "nope", json.RawMessage(`{}`))
			return nil, err
		},
	}
	tool := Tool(sb, []ai.Tool{newEchoTool()}, nil)

	_, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err == nil {
		t.Fatal("want error for unknown tool")
	}
	if !strings.Contains(err.Error(), "nope") || !strings.Contains(err.Error(), "echo") {
		t.Fatalf("err = %v, want it to mention nope and echo", err)
	}
	var toolExecErr *ai.ToolExecutionError
	if errors.As(err, &toolExecErr) {
		t.Fatalf("unknown tool name should not panic or produce a wrapped ToolExecutionError from the dispatcher itself: %v", err)
	}
}

func TestOutputTruncation(t *testing.T) {
	sb := &fakeSandbox{result: &Result{Output: "abcdefgh"}}
	tool := Tool(sb, nil, &Options{MaxOutputBytes: 5})

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "abcde\n[truncated]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestExecuteMalformedArgsWrapsInvalidToolArgumentsError pins finding 7:
// malformed {"code": ...} args must produce a typed
// *ai.InvalidToolArgumentsError (so errors.As works, and so
// GenerateTextOpts.RepairToolCall's bad-args repair path — which only
// triggers on that specific type — is offered a chance to fix it), not a
// raw encoding/json error.
func TestExecuteMalformedArgsWrapsInvalidToolArgumentsError(t *testing.T) {
	tool := Tool(&fakeSandbox{}, nil, nil)

	_, err := tool.Execute(t.Context(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("want error for malformed args")
	}
	var iae *ai.InvalidToolArgumentsError
	if !errors.As(err, &iae) {
		t.Fatalf("err = %v (%T), want *ai.InvalidToolArgumentsError", err, err)
	}
	if iae.ToolName != "run_code" {
		t.Fatalf("ToolName = %q, want run_code", iae.ToolName)
	}
}

func TestToolPanicsOnDuplicateToolName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("want panic on duplicate tool name")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "duplicate tool name") || !strings.Contains(msg, "echo") {
			t.Fatalf("panic message = %q, want it to mention duplicate tool name and echo", msg)
		}
	}()
	Tool(&fakeSandbox{}, []ai.Tool{newEchoTool(), newEchoTool()}, nil)
}

func TestOutputTruncationAtExactBoundary(t *testing.T) {
	sb := &fakeSandbox{result: &Result{Output: "abcde"}}
	tool := Tool(sb, nil, &Options{MaxOutputBytes: 5})

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "abcde" {
		t.Fatalf("got %q, want %q (no truncation, no suffix)", got, "abcde")
	}
}

func TestOutputTruncationDoesNotSplitMultiByteRune(t *testing.T) {
	// "é" is the 2-byte UTF-8 sequence 0xC3 0xA9. With a limit of 5 bytes,
	// "abcé" is 3 ASCII bytes + 2 bytes for é = 5 bytes exactly ("abc" +
	// "é"), then one more rune "X" pushes the total past the limit; the
	// cut must land on a rune boundary rather than slicing the "é" in
	// half.
	sb := &fakeSandbox{result: &Result{Output: "abcéX"}}
	tool := Tool(sb, nil, &Options{MaxOutputBytes: 4})

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Byte 4 (0-indexed) is the second byte of "é" (0xA9) -- not a rune
	// boundary, so the cut must back up to byte 3, keeping only "abc".
	want := "abc\n[truncated]"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if !utf8.ValidString(got.(string)) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
}

// TestNegativeMaxOutputBytesClampsToDefault pins finding 6: a negative
// Options.MaxOutputBytes must not reach truncateOutput as-is — cutting a
// string at a negative index panics (s[:negative]). <= 0 (not just == 0)
// must fall back to the documented default of 16384.
func TestNegativeMaxOutputBytesClampsToDefault(t *testing.T) {
	sb := &fakeSandbox{result: &Result{Output: "short"}}
	tool := Tool(sb, nil, &Options{MaxOutputBytes: -1})

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "short" {
		t.Fatalf("got %q, want %q (no truncation: well under the default limit)", got, "short")
	}
}

func TestNoTruncationWhenUnderLimit(t *testing.T) {
	sb := &fakeSandbox{result: &Result{Output: "short"}}
	tool := Tool(sb, nil, &Options{MaxOutputBytes: 100})

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "short" {
		t.Fatalf("got %q, want %q", got, "short")
	}
}

func TestLogsAppended(t *testing.T) {
	sb := &fakeSandbox{result: &Result{Output: "out", Logs: []string{"l1", "l2"}}}
	tool := Tool(sb, nil, nil)

	got, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "out\nlog: l1\nlog: l2"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSandboxErrorSurfacesAsPlainError(t *testing.T) {
	sentinel := errors.New("sandbox boom")
	sb := &fakeSandbox{err: sentinel}
	tool := Tool(sb, nil, nil)

	_, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel to surface via errors.Is", err)
	}
	var toolExecErr *ai.ToolExecutionError
	if errors.As(err, &toolExecErr) {
		t.Fatal("codemode's Tool must return the sandbox error verbatim, not pre-wrap it in *ai.ToolExecutionError")
	}
}

// TestDispatchRefusesApprovalRequiringTool pins finding 2: a tool wrapped in
// ai.RequireApproval must never execute through codemode's dispatch —
// sandboxed code has no suspension channel, so bypassing the approval gate
// would let model-written code silently perform an action a human was
// supposed to sign off on.
func TestDispatchRefusesApprovalRequiringTool(t *testing.T) {
	var executed bool
	guarded := ai.RequireApproval(ai.NewTool("danger", "", func(_ context.Context, _ echoArgs) (any, error) {
		executed = true
		return "boom", nil
	}))
	var callErr error
	sb := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			_, callErr = env.CallTool(ctx, "danger", json.RawMessage(`{"msg":"x"}`))
			return &Result{Output: "done"}, nil
		},
	}
	tool := Tool(sb, []ai.Tool{guarded}, nil)

	_, err := tool.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatalf("sandbox-level error unexpected: %v", err)
	}
	if callErr == nil {
		t.Fatal("want CallTool to return an error refusing the approval-requiring tool")
	}
	if executed {
		t.Fatal("tool must not execute when it requires approval")
	}
	if !strings.Contains(callErr.Error(), "danger") || !strings.Contains(callErr.Error(), "requires approval") {
		t.Fatalf("err = %v, want it to mention danger and requires approval", callErr)
	}
}

// TestDispatchConditionalApprovalWhenFuncConsulted pins finding 2's
// conditional case: RequireApproval's when-func must actually be consulted
// with the call's args, not treated as always-true (or always-false).
func TestDispatchConditionalApprovalWhenFuncConsulted(t *testing.T) {
	var executed bool
	guarded := ai.RequireApproval(ai.NewTool("maybe_danger", "", func(_ context.Context, _ echoArgs) (any, error) {
		executed = true
		return "ok", nil
	}), func(_ context.Context, args json.RawMessage) bool {
		var a echoArgs
		_ = json.Unmarshal(args, &a)
		return a.Msg == "risky"
	})

	// when=false: executes normally.
	sbSafe := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			res, err := env.CallTool(ctx, "maybe_danger", json.RawMessage(`{"msg":"safe"}`))
			if err != nil {
				return nil, err
			}
			return &Result{Output: fmt.Sprintf("%v", res)}, nil
		},
	}
	toolSafe := Tool(sbSafe, []ai.Tool{guarded}, nil)
	got, err := toolSafe.Execute(t.Context(), json.RawMessage(`{"code":"x"}`))
	if err != nil {
		t.Fatalf("safe call should execute: %v", err)
	}
	if !executed {
		t.Fatal("safe call should have executed")
	}
	if got != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}

	// when=true: refused.
	executed = false
	var callErr error
	sbRisky := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			_, callErr = env.CallTool(ctx, "maybe_danger", json.RawMessage(`{"msg":"risky"}`))
			return &Result{Output: "done"}, nil
		},
	}
	toolRisky := Tool(sbRisky, []ai.Tool{guarded}, nil)
	if _, err := toolRisky.Execute(t.Context(), json.RawMessage(`{"code":"x"}`)); err != nil {
		t.Fatalf("sandbox-level error unexpected: %v", err)
	}
	if callErr == nil {
		t.Fatal("want error refusing the risky call")
	}
	if executed {
		t.Fatal("risky call must not execute")
	}
}

func TestRuntimeContextVisibleInsideDispatchedTool(t *testing.T) {
	var seen ai.RuntimeContext
	rcTool := ai.NewTool("check_rc", "", func(ctx context.Context, _ echoArgs) (any, error) {
		seen = ai.RuntimeContextFrom(ctx)
		return "ok", nil
	})
	sb := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			_, err := env.CallTool(ctx, "check_rc", json.RawMessage(`{"msg":""}`))
			return &Result{Output: "done"}, err
		},
	}
	tool := Tool(sb, []ai.Tool{rcTool}, nil)

	m := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.ToolCallPart{
			ID: "c1", Name: "run_code", Args: []byte(`{"code":"check_rc({})"}`)}},
			FinishReason: provider.FinishToolCalls, Usage: provider.Usage{TotalTokens: 10}},
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
	}}

	rc := ai.RuntimeContext{"tenant": "acme"}
	_, err := ai.GenerateText(t.Context(), ai.GenerateTextOpts{
		Model: m, Prompt: "go", Tools: []ai.Tool{tool}, MaxSteps: 3,
		RuntimeContext: rc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen["tenant"] != "acme" {
		t.Fatalf("RuntimeContext not visible inside dispatched tool: %+v", seen)
	}
}

func TestEndToEndThroughGenerateText(t *testing.T) {
	sb := &fakeSandbox{
		callback: func(ctx context.Context, env Env) (*Result, error) {
			res, err := env.CallTool(ctx, "echo", json.RawMessage(`{"msg":"world"}`))
			if err != nil {
				return nil, err
			}
			return &Result{Output: fmt.Sprintf("%v", res)}, nil
		},
	}
	runCode := Tool(sb, []ai.Tool{newEchoTool()}, nil)

	m := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.ToolCallPart{
			ID: "c1", Name: "run_code", Args: []byte(`{"code":"echo({msg: 'world'})"}`)}},
			FinishReason: provider.FinishToolCalls, Usage: provider.Usage{TotalTokens: 10}},
		{Content: []provider.ContentPart{provider.TextPart{Text: "The answer is echoed: world"}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
	}}

	res, err := ai.GenerateText(t.Context(), ai.GenerateTextOpts{
		Model: m, Prompt: "run some code", Tools: []ai.Tool{runCode}, MaxSteps: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) == 0 || len(res.Steps[0].ToolResults) == 0 {
		t.Fatal("expected a tool result step")
	}
	tr := res.Steps[0].ToolResults[0]
	if tr.Err != nil {
		t.Fatalf("tool result error: %v", tr.Err)
	}
	if tr.Result != "echoed: world" {
		t.Fatalf("tool result = %v, want %q", tr.Result, "echoed: world")
	}

	// What the model saw on the follow-up call must carry that same text.
	second := m.Calls[1].Messages
	toolMsg := second[len(second)-1]
	if toolMsg.Role != provider.RoleTool {
		t.Fatal("expected trailing tool result message")
	}
	trp, ok := toolMsg.Content[0].(provider.ToolResultPart)
	if !ok {
		t.Fatalf("expected ToolResultPart, got %T", toolMsg.Content[0])
	}
	if !strings.Contains(fmt.Sprintf("%v", trp.Result), "echoed: world") {
		t.Fatalf("tool result part = %+v, want it to contain %q", trp, "echoed: world")
	}
}
