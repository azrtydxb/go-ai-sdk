package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// ErrSubAgentSuspended is the Cause of the *ai.ToolExecutionError AsTool's
// Execute returns when the sub-agent's own run suspends (its
// ai.GenerateTextResult.PendingApprovals is non-empty) rather than
// completing. There is no resume channel through a tool call — the parent's
// tool loop has no way to surface the sub-agent's PendingApprovals or later
// supply Approvals for the sub-agent's run — so a suspended sub-agent must
// decide its approvals inline, via Agent.ApproveToolCall, rather than
// suspend-and-resume like a top-level GenerateText/StreamText call would.
// Check for it with errors.Is.
var ErrSubAgentSuspended = errors.New("agent: sub-agent run suspended pending tool approval")

// asToolArgs is the {"task": string} argument shape AsTool's schema
// describes and its Execute decodes.
type asToolArgs struct {
	Task string `json:"task"`
}

// asToolSchemaTemplate is the hand-written JSON Schema for AsTool's
// generated tool, with "<name>" substituted for the sub-agent's name in the
// description.
const asToolSchemaTemplate = `{"type":"object","properties":{"task":{"type":"string","description":"The task for the %s agent."}},"required":["task"],"additionalProperties":false}`

// asToolTool is the ai.Tool implementation AsTool returns.
type asToolTool struct {
	agent       *Agent
	name        string
	description string
	schema      json.RawMessage
}

// AsTool exposes a as an ai.Tool so a parent agent can delegate to it. The
// returned tool takes {"task": string} and, when called, runs a with
// RunOpts{Prompt: task} and returns the sub-agent's decoded Output when it
// has one, or its final Text otherwise. A sub-agent error is wrapped in a
// *ai.ToolExecutionError (ToolName set to the tool's own name, Cause the
// sub-agent's error) before it is returned, matching the error taxonomy
// ai.NewTool-built tools already produce for a failing handler — so a
// failing sub-agent looks like any other failing tool to the parent's loop
// and to code that type-switches/errors.As on ToolResultRecord.Err.
//
// Suspension: a sub-agent run via AsTool cannot suspend and resume the way
// a top-level Agent/ai.GenerateText call can — see ErrSubAgentSuspended's
// doc. Sub-agents must decide their own tools' approvals inline, via
// a.ApproveToolCall.
//
// RuntimeContext scoping: ai.RuntimeContext installs once per
// GenerateText/StreamText call, and installing a nil RuntimeContext is a
// no-op (see ai.RuntimeContextFrom) — it leaves whatever was already on
// ctx untouched rather than clearing it. That gives AsTool inherited
// scoping by default: when a (the sub-agent) has RuntimeContext unset, the
// ctx passed to a.Generate already carries the PARENT's RuntimeContext
// (installed by the parent's own GenerateText/StreamText call before any
// tool executes), so a's own tools see the parent's RuntimeContext via
// ai.RuntimeContextFrom. Setting a.RuntimeContext overrides this: a
// non-nil value (even an explicitly empty ai.RuntimeContext{}) always
// installs and shadows whatever was on ctx for the duration of a's run —
// so an empty ai.RuntimeContext{} is the way to explicitly isolate a
// sub-agent's tools from the parent's RuntimeContext rather than merely
// leaving it unset.
func AsTool(a *Agent, name, description string) ai.Tool {
	return &asToolTool{
		agent:       a,
		name:        name,
		description: description,
		schema:      json.RawMessage(fmt.Sprintf(asToolSchemaTemplate, name)),
	}
}

// Name implements ai.Tool.
func (t *asToolTool) Name() string { return t.name }

// Description implements ai.Tool.
func (t *asToolTool) Description() string { return t.description }

// Schema implements ai.Tool.
func (t *asToolTool) Schema() json.RawMessage { return t.schema }

// Execute implements ai.Tool.
func (t *asToolTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	var a asToolArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, &ai.InvalidToolArgumentsError{ToolName: t.name, Args: args, Cause: err}
	}

	result, err := t.agent.Generate(ctx, RunOpts{Prompt: a.Task})
	if err != nil {
		return nil, &ai.ToolExecutionError{ToolName: t.name, Cause: err}
	}

	if len(result.PendingApprovals) > 0 {
		// A suspended sub-agent run is not a successful "" result — see
		// ErrSubAgentSuspended's doc for why this can't be resumed through
		// a tool call the way a top-level suspension can.
		return nil, &ai.ToolExecutionError{ToolName: t.name, Cause: ErrSubAgentSuspended}
	}

	if result.Output != nil {
		return result.Output, nil
	}
	return result.Text, nil
}
