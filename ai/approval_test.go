package ai

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

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
		Approvals: []ApprovalDecision{{ToolCallID: "c2", Approved: true}},
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
