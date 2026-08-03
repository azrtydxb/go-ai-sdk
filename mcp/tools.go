package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// mcpTool adapts one MCP ToolDef to the ai.Tool interface: Name/Description/
// Schema pass through the server's definition verbatim (the schema is not
// re-derived), and Execute forwards the raw JSON arguments straight to
// CallTool.
type mcpTool struct {
	client *Client
	def    ToolDef
}

// Tools lists the server's tools and adapts each to an ai.Tool whose
// Execute calls CallTool; a ToolResult with IsError=true becomes a Go error
// (so the ai loop records it as a failed tool call). Name/Description/
// schema pass through verbatim (schema NOT re-derived).
func Tools(ctx context.Context, c *Client) ([]ai.Tool, error) {
	defs, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	tools := make([]ai.Tool, len(defs))
	for i, d := range defs {
		tools[i] = &mcpTool{client: c, def: d}
	}
	return tools, nil
}

// Name implements ai.Tool.
func (t *mcpTool) Name() string { return t.def.Name }

// Description implements ai.Tool.
func (t *mcpTool) Description() string { return t.def.Description }

// Schema implements ai.Tool.
func (t *mcpTool) Schema() json.RawMessage { return t.def.InputSchema }

// Strict always returns false: MCP tool definitions have no strict-mode
// concept.
func (t *mcpTool) Strict() bool { return false }

// InputExamples always returns nil: MCP tool definitions carry no example
// inputs.
func (t *mcpTool) InputExamples() []json.RawMessage { return nil }

// InputCallbacks always returns a zero-value ai.ToolInputCallbacks: MCP
// tools have no per-tool input-streaming lifecycle hooks.
func (t *mcpTool) InputCallbacks() ai.ToolInputCallbacks { return ai.ToolInputCallbacks{} }

// Execute implements ai.Tool. args passes through as raw JSON, unmodified;
// it is not unmarshaled here since the server owns the schema. Both
// transport failures and a ToolResult with IsError=true are turned into an
// *ai.ToolExecutionError (ToolName set to the MCP tool's name, Cause
// carrying the underlying error or the result's text), so the ai tool loop
// records the call as a failed tool call rather than a success and callers
// can recover the structured error via errors.As.
func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	res, err := t.client.CallTool(ctx, t.def.Name, args)
	if err != nil {
		return nil, &ai.ToolExecutionError{ToolName: t.def.Name, Cause: err}
	}
	if res.IsError {
		return nil, &ai.ToolExecutionError{ToolName: t.def.Name, Cause: fmt.Errorf("%s", res.Text)}
	}
	return res.Text, nil
}
