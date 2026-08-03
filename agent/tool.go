package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

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
// has one, or its final Text otherwise. A sub-agent error propagates as the
// tool's execution error — the parent's tool loop wraps it in a
// *ai.ToolExecutionError, same as any other failing tool.
//
// The parent's ai.RuntimeContext flows into the sub-agent's ctx (it's just
// the same ctx passed to Execute) only insofar as the sub-agent doesn't
// install its own: a always installs its own RuntimeContext (nil if unset)
// for its own run, per ai.GenerateTextOpts.RuntimeContext — so it is a's
// RuntimeContext, not the parent's, that governs what a's own tools see via
// ai.RuntimeContextFrom, regardless of what the parent had installed.
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
		return nil, err
	}

	result, err := t.agent.Generate(ctx, RunOpts{Prompt: a.Task})
	if err != nil {
		return nil, err
	}

	if result.Output != nil {
		return result.Output, nil
	}
	return result.Text, nil
}
