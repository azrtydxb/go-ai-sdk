package ai

import (
	"context"
	"encoding/json"
)

// ApprovalRequirer is implemented by Tools whose calls need approval before
// execution. RequireApproval wraps any Tool to add it. GenerateText and
// StreamText check every tool call's underlying Tool for this interface
// (after unwrapping via RequireApproval's wrapper, since that's what's
// actually stored in GenerateTextOpts.Tools) and resolve a decision for each
// one that needs approval — see GenerateTextOpts.ApproveToolCall and
// Approvals for how a decision is reached, and PendingApprovals for what
// happens when none is available.
type ApprovalRequirer interface {
	ApprovalRequired(ctx context.Context, args json.RawMessage) bool
}

// RequireApproval wraps t so every call requires approval; with a non-nil
// when func (only the first is used; it's variadic purely so the argument
// can be omitted), only calls for which when returns true do. The ctx passed
// to when carries the same RuntimeContext installed for the run (see
// RuntimeContextFrom).
func RequireApproval(t Tool, when ...func(ctx context.Context, args json.RawMessage) bool) Tool {
	at := &approvalTool{Tool: t}
	if len(when) > 0 {
		at.when = when[0]
	}
	return at
}

// approvalTool wraps a Tool to implement ApprovalRequirer, per RequireApproval.
type approvalTool struct {
	Tool
	when func(ctx context.Context, args json.RawMessage) bool
}

// ApprovalRequired implements ApprovalRequirer.
func (t *approvalTool) ApprovalRequired(ctx context.Context, args json.RawMessage) bool {
	if t.when == nil {
		return true
	}
	return t.when(ctx, args)
}

// ApprovalRequest describes one tool call awaiting an approval decision:
// passed to GenerateTextOpts.ApproveToolCall, and reported (for calls left
// undecided) on GenerateTextResult.PendingApprovals.
type ApprovalRequest struct {
	StepIndex int
	Call      ToolCallRecord
}

// ApprovalDecision is a resolved approval outcome for one tool call, matched
// by ToolCallID: supplied out-of-band via GenerateTextOpts.Approvals on a
// resume call, or returned inline by GenerateTextOpts.ApproveToolCall.
type ApprovalDecision struct {
	ToolCallID string
	Approved   bool
	Reason     string // included in the denial tool result sent to the model
}

// findApprovalDecision returns the first ApprovalDecision in approvals whose
// ToolCallID matches id — see GenerateTextOpts.Approvals's matching rule.
func findApprovalDecision(approvals []ApprovalDecision, id string) (ApprovalDecision, bool) {
	for _, d := range approvals {
		if d.ToolCallID == id {
			return d, true
		}
	}
	return ApprovalDecision{}, false
}
