package ai

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func approvalWeatherTool(instrument *bool) Tool {
	return NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		if instrument != nil {
			*instrument = true
		}
		return "sunny", nil
	})
}

// ---------------------------------------------------------------------
// RequireApproval wrapping
// ---------------------------------------------------------------------

func TestRequireApprovalStaticAlwaysRequires(t *testing.T) {
	base := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	wrapped := RequireApproval(base)
	ar, ok := wrapped.(ApprovalRequirer)
	if !ok {
		t.Fatal("wrapped tool does not implement ApprovalRequirer")
	}
	if !ar.ApprovalRequired(t.Context(), json.RawMessage(`{}`)) {
		t.Fatal("static RequireApproval should always require approval")
	}
	// The wrapped tool must still behave like the original.
	if wrapped.Name() != "t" {
		t.Fatalf("Name() = %q", wrapped.Name())
	}
}

func TestRequireApprovalConditionalWhen(t *testing.T) {
	base := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	wrapped := RequireApproval(base, func(_ context.Context, args json.RawMessage) bool {
		var a weatherArgs
		_ = json.Unmarshal(args, &a)
		return a.City == "Ghent"
	})
	ar := wrapped.(ApprovalRequirer)
	if !ar.ApprovalRequired(t.Context(), []byte(`{"city":"Ghent"}`)) {
		t.Fatal("Ghent should require approval")
	}
	if ar.ApprovalRequired(t.Context(), []byte(`{"city":"Bruges"}`)) {
		t.Fatal("Bruges should not require approval")
	}
}

// ---------------------------------------------------------------------
// Inline ApproveToolCall: approve / deny
// ---------------------------------------------------------------------

func TestApproveToolCallApprovedExecutes(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		ApproveToolCall: func(_ context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: true}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executed {
		t.Fatal("tool should have executed")
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("result = %+v", res.Steps[0].ToolResults[0])
	}
}

func TestApproveToolCallDeniedRecordsErrorAndSkipsExecute(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		ApproveToolCall: func(_ context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: false, Reason: "not allowed"}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool must not execute when denied")
	}
	var denied *ToolApprovalDeniedError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &denied) {
		t.Fatalf("err = %v, want *ToolApprovalDeniedError", res.Steps[0].ToolResults[0].Err)
	}
	if denied.Reason != "not allowed" {
		t.Fatalf("Reason = %q", denied.Reason)
	}

	// The RoleTool message sent to the model carries the denial text with
	// IsError set.
	second := m.Calls[1].Messages
	tr := second[len(second)-1].Content[0].(provider.ToolResultPart)
	if !tr.IsError {
		t.Fatal("IsError not set")
	}
	wantText := `ai: tool "get_weather" execution denied: not allowed`
	if tr.Result != wantText {
		t.Fatalf("Result = %q, want %q", tr.Result, wantText)
	}
}

// ---------------------------------------------------------------------
// Pending suspension
// ---------------------------------------------------------------------

func TestPendingSuspendsWithNoExecuteCall(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool must not execute while pending")
	}
	if len(res.PendingApprovals) != 1 {
		t.Fatalf("PendingApprovals = %+v", res.PendingApprovals)
	}
	if res.PendingApprovals[0].Call.ID != "c1" || res.PendingApprovals[0].Call.Name != "get_weather" {
		t.Fatalf("PendingApprovals[0] = %+v", res.PendingApprovals[0])
	}
	if res.FinishReason != provider.FinishToolCalls {
		t.Fatalf("FinishReason = %v", res.FinishReason)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Role != provider.RoleAssistant {
		t.Fatalf("last message role = %v, want assistant", last.Role)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1 (tool-result-less final step)", len(res.Steps))
	}
	if res.Steps[0].ToolResults != nil {
		t.Fatalf("Steps[0].ToolResults = %+v, want nil", res.Steps[0].ToolResults)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (no second call while pending)", len(m.Calls))
	}
}

func TestPendingSuspensionStreamText(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
	}}
	var finished *GenerateTextResult
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
		OnFinish: func(r *GenerateTextResult) { finished = r },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if executed {
		t.Fatal("tool must not execute while pending")
	}
	if len(s.PendingApprovals()) != 1 {
		t.Fatalf("PendingApprovals = %+v", s.PendingApprovals())
	}
	if finished == nil {
		t.Fatal("OnFinish did not fire")
	}
	if len(finished.PendingApprovals) != 1 {
		t.Fatalf("OnFinish result PendingApprovals = %+v", finished.PendingApprovals)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.Calls))
	}
}

// ---------------------------------------------------------------------
// Resume
// ---------------------------------------------------------------------

func pendingAssistantMessages(prompt string) []provider.Message {
	return []provider.Message{
		provider.UserText(prompt),
		{Role: provider.RoleAssistant, Content: []provider.ContentPart{
			provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)},
		}},
	}
}

func TestResumeWithApprovalsExecutesAndContinues(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		Approvals: []ApprovalDecision{{ToolCallID: "c1", Approved: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executed {
		t.Fatal("tool should execute on resume")
	}
	if len(m.Calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (continues to a second model step)", len(m.Calls))
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q", res.Text)
	}
	// the resumed batch's RoleTool message must precede the new model call
	sent := m.Calls[0].Messages
	if sent[len(sent)-1].Role != provider.RoleTool {
		t.Fatalf("last message sent = %v, want RoleTool", sent[len(sent)-1].Role)
	}
	if len(res.PendingApprovals) != 0 {
		t.Fatalf("PendingApprovals = %+v, want none", res.PendingApprovals)
	}
}

func TestResumeWithDenial(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.TextPart{Text: "denied, sorry"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		Approvals: []ApprovalDecision{{ToolCallID: "c1", Approved: false, Reason: "policy"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool must not execute on denial")
	}
	sent := m.Calls[0].Messages
	tr := sent[len(sent)-1].Content[0].(provider.ToolResultPart)
	if !tr.IsError {
		t.Fatal("IsError not set for resumed denial")
	}
	if res.Text != "denied, sorry" {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestRepeatedSuspensionOnResume(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{} // no model calls expected at all
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		// No Approvals, no ApproveToolCall: still pending.
	})
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool must not execute")
	}
	if len(res.PendingApprovals) != 1 || res.PendingApprovals[0].Call.ID != "c1" {
		t.Fatalf("PendingApprovals = %+v", res.PendingApprovals)
	}
	if len(m.Calls) != 0 {
		t.Fatalf("model calls = %d, want 0", len(m.Calls))
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Role != provider.RoleAssistant {
		t.Fatalf("last message role = %v", last.Role)
	}
}

func TestResumeStreamText(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.TextDelta{Text: "done"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		Approvals: []ApprovalDecision{{ToolCallID: "c1", Approved: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if !executed {
		t.Fatal("tool should execute on resume")
	}
	if s.Text() != "done" {
		t.Fatalf("Text = %q", s.Text())
	}
	if len(m.Calls) != 1 {
		t.Fatalf("model calls = %d, want 1", len(m.Calls))
	}
}

// TestResumeStreamTextImmediateSuspendReleasesTotalTimeout pins the fix for
// the resume-batch immediate-suspend path leaking Timeout.Total's derived
// context/timer: when the resume batch still has no decision (still
// pending), StreamText returns *TextStream with s.current == nil and
// pendingApprovals set, without ever starting a model stream. A caller that
// reads PendingApprovals() and drops the stream — never calling Parts() or
// Close() — must not leak Total's context/timer. Since s.ctx is an
// unexported field, this test (same package) asserts it directly: it must
// already be Done immediately after StreamText returns, proving Total's
// timer/goroutine was released eagerly rather than left to fire on its own
// after the (here, very long) Total duration.
func TestResumeStreamTextImmediateSuspendReleasesTotalTimeout(t *testing.T) {
	tool := RequireApproval(approvalWeatherTool(nil))
	m := &aitest.MockModel{} // no model calls expected at all
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		Timeout: &Timeout{Total: time.Hour}, // far larger than the test; only released early if the fix works
		// No Approvals, no ApproveToolCall: still pending.
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.PendingApprovals()) != 1 {
		t.Fatalf("PendingApprovals = %+v", s.PendingApprovals())
	}
	// The caller drops s here without calling Parts() or Close() — exactly
	// the leak scenario. s.ctx must already be canceled/done: Total's
	// context.WithTimeoutCause-derived ctx/timer was released proactively.
	select {
	case <-s.ctx.Done():
	default:
		t.Fatal("s.ctx is not done: Timeout.Total's context/timer was not released eagerly (leak)")
	}
}

// TestResumeStreamTextImmediateSuspendThenPartsStillFinishes confirms the
// eager-release fix above doesn't regress the ordinary case where a caller
// DOES go on to call Parts() after reading (or ignoring) PendingApprovals():
// OnFinish must still fire normally (not as an abort), since Timeout.Total
// never actually elapsed — only our own proactive early release made
// s.ctx.Err() non-nil, which finishOrTimeout must not mistake for a real
// timeout or a caller abort.
func TestResumeStreamTextImmediateSuspendThenPartsStillFinishes(t *testing.T) {
	tool := RequireApproval(approvalWeatherTool(nil))
	m := &aitest.MockModel{}
	var finished *GenerateTextResult
	var aborted bool
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Messages: pendingAssistantMessages("weather?"), Tools: []Tool{tool}, MaxSteps: 2,
		Timeout:  &Timeout{Total: time.Hour},
		OnFinish: func(r *GenerateTextResult) { finished = r },
		OnAbort:  func() { aborted = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatalf("s.Err() = %v, want nil (no real timeout ever fired)", s.Err())
	}
	if aborted {
		t.Fatal("OnAbort fired; want normal finish (the eager release must not be mistaken for a real abort)")
	}
	if finished == nil {
		t.Fatal("OnFinish did not fire")
	}
	if len(finished.PendingApprovals) != 1 {
		t.Fatalf("OnFinish result PendingApprovals = %+v", finished.PendingApprovals)
	}
}

// ---------------------------------------------------------------------
// Mixed batch / all-decided batch
// ---------------------------------------------------------------------

func TestMixedBatchApprovalAndPlainToolSuspendsEverything(t *testing.T) {
	var plainExecuted, approvalExecuted bool
	plain := NewTool("plain", "", func(_ context.Context, a weatherArgs) (any, error) {
		plainExecuted = true
		return "r", nil
	})
	approvalTool := RequireApproval(approvalWeatherTool(&approvalExecuted))

	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content: []provider.ContentPart{
				provider.ToolCallPart{ID: "c1", Name: "plain", Args: []byte(`{"city":"a"}`)},
				provider.ToolCallPart{ID: "c2", Name: "get_weather", Args: []byte(`{"city":"b"}`)},
			},
			FinishReason: provider.FinishToolCalls,
		},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{plain, approvalTool}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plainExecuted || approvalExecuted {
		t.Fatal("neither tool should execute when the batch has a pending call")
	}
	if len(res.PendingApprovals) != 1 || res.PendingApprovals[0].Call.ID != "c2" {
		t.Fatalf("PendingApprovals = %+v", res.PendingApprovals)
	}
}

func TestBatchWithDecisionsForAllExecutesAll(t *testing.T) {
	var plainExecuted, approvalExecuted bool
	plain := NewTool("plain", "", func(_ context.Context, a weatherArgs) (any, error) {
		plainExecuted = true
		return "r", nil
	})
	approvalTool := RequireApproval(approvalWeatherTool(&approvalExecuted))

	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content: []provider.ContentPart{
				provider.ToolCallPart{ID: "c1", Name: "plain", Args: []byte(`{"city":"a"}`)},
				provider.ToolCallPart{ID: "c2", Name: "get_weather", Args: []byte(`{"city":"b"}`)},
			},
			FinishReason: provider.FinishToolCalls,
		},
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{plain, approvalTool}, MaxSteps: 2,
		// This is the FIRST batch of a Prompt-driven run, not a resume
		// batch (see finding 5 / TestApprovalsDoesNotAutoApproveLaterBatchWithCollidingID):
		// opts.Approvals is only consulted for the resume batch, so a
		// non-resume batch's decision must come from ApproveToolCall
		// instead.
		ApproveToolCall: func(_ context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: true}, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plainExecuted || !approvalExecuted {
		t.Fatal("both tools should execute once every call is decided")
	}
	if len(res.PendingApprovals) != 0 {
		t.Fatalf("PendingApprovals = %+v, want none", res.PendingApprovals)
	}
}

// ---------------------------------------------------------------------
// Lifecycle events per rule 2/3
// ---------------------------------------------------------------------

func TestLifecycleEventsNotFiredForPendingBatch(t *testing.T) {
	tool := RequireApproval(approvalWeatherTool(nil))
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
	}}
	var starts, ends int
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		OnToolExecutionStart: func(int, ToolCallRecord) { starts++ },
		OnToolExecutionEnd:   func(int, ToolResultRecord, error) { ends++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 0 || ends != 0 {
		t.Fatalf("starts=%d ends=%d, want 0/0 for an unexecuted pending batch", starts, ends)
	}
}

func TestLifecycleEventsFiredForDeniedCall(t *testing.T) {
	tool := RequireApproval(approvalWeatherTool(nil))
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	var starts, ends int
	var endErr error
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		ApproveToolCall: func(_ context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: false, Reason: "no"}, true
		},
		OnToolExecutionStart: func(int, ToolCallRecord) { starts++ },
		OnToolExecutionEnd: func(_ int, _ ToolResultRecord, err error) {
			ends++
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("starts=%d ends=%d, want 1/1 for a denied (executed-batch) call", starts, ends)
	}
	var denied *ToolApprovalDeniedError
	if !errors.As(endErr, &denied) {
		t.Fatalf("OnToolExecutionEnd err = %v, want *ToolApprovalDeniedError", endErr)
	}
}

// ---------------------------------------------------------------------
// Finding 5: Approvals is scoped to the resume batch only
// ---------------------------------------------------------------------

// TestApprovalsDoesNotAutoApproveLaterBatchWithColliding ID pins finding 5:
// some providers (e.g. geminicompat) synthesize deterministic tool-call IDs
// like call_<name>_<index> — so a LATER batch, arising after the resume
// batch within the SAME run, can legitimately reuse the exact ToolCallID an
// earlier Approvals entry already answered. Matching opts.Approvals by ID
// alone against every batch in the run (not just the resume batch it was
// supplied for) would silently auto-approve that unrelated later call with a
// decision that was never actually made for it.
func TestApprovalsDoesNotAutoApproveLaterBatchWithCollidingID(t *testing.T) {
	var executions int
	guarded := RequireApproval(NewTool("guarded", "", func(_ context.Context, a weatherArgs) (any, error) {
		executions++
		return "ok", nil
	}))

	// The resume batch: an unanswered assistant tool-call message ending in
	// a call to "guarded" with ID "call_guarded_0", answered by Approvals.
	messages := []provider.Message{
		provider.UserText("go"),
		{Role: provider.RoleAssistant, Content: []provider.ContentPart{
			provider.ToolCallPart{ID: "call_guarded_0", Name: "guarded", Args: []byte(`{"city":"a"}`)},
		}},
	}

	m := &aitest.MockModel{Responses: []*provider.Response{
		// Step 0 of the NEW run (post-resume): a LATER call that happens to
		// reuse the same ID a synthesizing provider could plausibly produce
		// again (e.g. index resets, or a second run reusing the pattern).
		toolCallResponse("guarded", "call_guarded_0", `{"city":"b"}`),
		// Only reached if the bug lets the later call auto-approve and
		// execute, continuing the loop for one more (unwanted) step.
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}

	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Messages: messages, Tools: []Tool{guarded}, MaxSteps: 3,
		Approvals: []ApprovalDecision{{ToolCallID: "call_guarded_0", Approved: true}},
		// No ApproveToolCall: the later batch has no other way to get a
		// decision, so it must suspend rather than reuse the resume
		// decision.
	})
	if err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want 1 (only the resume batch's call)", executions)
	}
	if len(res.PendingApprovals) != 1 || res.PendingApprovals[0].Call.ID != "call_guarded_0" {
		t.Fatalf("PendingApprovals = %+v, want the later call suspended", res.PendingApprovals)
	}
}

// TestApprovalsStillHonoredOnResumeBatch is the control: the SAME Approvals
// field must still work normally for the resume batch itself.
func TestApprovalsStillHonoredOnResumeBatch(t *testing.T) {
	var executions int
	guarded := RequireApproval(NewTool("guarded", "", func(_ context.Context, a weatherArgs) (any, error) {
		executions++
		return "ok", nil
	}))
	messages := []provider.Message{
		provider.UserText("go"),
		{Role: provider.RoleAssistant, Content: []provider.ContentPart{
			provider.ToolCallPart{ID: "call_guarded_0", Name: "guarded", Args: []byte(`{"city":"a"}`)},
		}},
	}
	m := &aitest.MockModel{Responses: []*provider.Response{
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Messages: messages, Tools: []Tool{guarded}, MaxSteps: 2,
		Approvals: []ApprovalDecision{{ToolCallID: "call_guarded_0", Approved: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want 1", executions)
	}
	if len(res.PendingApprovals) != 0 {
		t.Fatalf("PendingApprovals = %+v, want none", res.PendingApprovals)
	}
	if res.Text != "done" {
		t.Fatalf("Text = %q", res.Text)
	}
}

// ---------------------------------------------------------------------
// Finding 3: repair must re-consult ApprovalRequired on the repaired call
// ---------------------------------------------------------------------

// TestBadArgsRepairRenamingToApprovalToolIsDenied pins finding 3's
// rename-repro: RepairToolCall's bad-args repair path may rename a call to a
// DIFFERENT tool. If that tool now requires approval, executing it directly
// (as the pre-fix code did) bypasses the approval gate entirely — there is
// no suspension possible mid-execution, so the repaired call must instead be
// recorded as a denial.
func TestBadArgsRepairRenamingToApprovalToolIsDenied(t *testing.T) {
	var addExecuted, weatherExecuted bool
	addTool := NewTool("add_tool", "", func(_ context.Context, a weatherArgs) (any, error) {
		addExecuted = true
		return "added", nil
	})
	guarded := RequireApproval(approvalWeatherTool(&weatherExecuted))

	m := &aitest.MockModel{Responses: []*provider.Response{
		// "bogus" is not a field of weatherArgs -> strict decode fails with
		// *InvalidToolArgumentsError, offering RepairToolCall a chance.
		toolCallResponse("add_tool", "c1", `{"bogus":1}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{addTool, guarded}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			var iae *InvalidToolArgumentsError
			if errors.As(toolErr, &iae) {
				return ToolCallRecord{ID: call.ID, Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)}, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if addExecuted || weatherExecuted {
		t.Fatal("neither the original nor the repaired-to tool should execute when the repaired call requires approval")
	}
	var denied *ToolApprovalDeniedError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &denied) {
		t.Fatalf("err = %v, want *ToolApprovalDeniedError", res.Steps[0].ToolResults[0].Err)
	}
	if denied.ToolName != "get_weather" {
		t.Fatalf("denied.ToolName = %q, want get_weather", denied.ToolName)
	}
}

// TestBadArgsRepairChangingApprovalToolArgsIsDenied pins finding 3's
// changed-args repro: the ORIGINAL call already targeted an
// approval-requiring tool and was approved (for its original args), but
// RepairToolCall's bad-args repair then changed those args. The approval
// that was granted covered the original args, not the repaired ones — the
// repaired call must be re-checked (and, here, re-denied) rather than riding
// on the stale decision.
func TestBadArgsRepairChangingApprovalToolArgsIsDenied(t *testing.T) {
	var weatherExecuted bool
	guarded := RequireApproval(approvalWeatherTool(&weatherExecuted))

	var approveCalls int
	m := &aitest.MockModel{Responses: []*provider.Response{
		// "bogus" is an unknown field -> *InvalidToolArgumentsError from
		// Execute, even though ApprovalRequired (a permissive partial
		// unmarshal) still resolves City fine from the same JSON.
		toolCallResponse("get_weather", "c1", `{"city":"Ghent","bogus":1}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{guarded}, MaxSteps: 2,
		ApproveToolCall: func(_ context.Context, req ApprovalRequest) (ApprovalDecision, bool) {
			approveCalls++
			return ApprovalDecision{ToolCallID: req.Call.ID, Approved: true}, true
		},
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			var iae *InvalidToolArgumentsError
			if errors.As(toolErr, &iae) {
				call.Args = []byte(`{"city":"Bruges"}`)
				return call, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if weatherExecuted {
		t.Fatal("repaired call must not execute: its (changed) args were never approved")
	}
	if approveCalls != 1 {
		t.Fatalf("ApproveToolCall calls = %d, want 1 (only for the original call)", approveCalls)
	}
	var denied *ToolApprovalDeniedError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &denied) {
		t.Fatalf("err = %v, want *ToolApprovalDeniedError", res.Steps[0].ToolResults[0].Err)
	}
}

// TestBadArgsRepairNonApprovalToolStillWorks verifies the fix is scoped:
// bad-args repair for a tool that never requires approval executes normally
// even when an unrelated approval-requiring tool is present in Tools.
func TestBadArgsRepairNonApprovalToolStillWorks(t *testing.T) {
	guarded := RequireApproval(approvalWeatherTool(nil))
	plain := NewTool("plain", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "plain result for " + a.City, nil
	})
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("plain", "c1", `{"bogus":1}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{plain, guarded}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
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
	if res.Steps[0].ToolResults[0].Err != nil {
		t.Fatalf("err = %v, want nil (non-approval repair unaffected)", res.Steps[0].ToolResults[0].Err)
	}
	if res.Steps[0].ToolResults[0].Result != "plain result for Ghent" {
		t.Fatalf("result = %v", res.Steps[0].ToolResults[0].Result)
	}
}

// ---------------------------------------------------------------------
// Race test on the StreamText suspension path
// ---------------------------------------------------------------------

func TestStreamTextSuspensionRace(t *testing.T) {
	tool := RequireApproval(approvalWeatherTool(nil))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := &aitest.MockModel{Streams: [][]provider.StreamPart{
				{
					provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)}},
					provider.FinishPart{Reason: provider.FinishToolCalls},
				},
			}}
			s, err := StreamText(context.Background(), GenerateTextOpts{
				Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
			})
			if err != nil {
				t.Error(err)
				return
			}
			for range s.Parts() {
			}
			if s.Err() != nil {
				t.Error(s.Err())
			}
			if len(s.PendingApprovals()) != 1 {
				t.Errorf("PendingApprovals = %+v", s.PendingApprovals())
			}
		}()
	}
	wg.Wait()
}
