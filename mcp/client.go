package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// protocolVersion is the MCP protocol version this client speaks.
const protocolVersion = "2025-03-26"

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      clientInfo `json:"clientInfo"`
	Capabilities    struct{}   `json:"capabilities"`
}

// Initialize performs the MCP handshake: an "initialize" request, followed
// by a "notifications/initialized" notification once the server replies.
func (c *Client) Initialize(ctx context.Context) error {
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      clientInfo{Name: "go-ai-sdk", Version: "0.1"},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: notifications/initialized: %w", err)
	}
	return nil
}

// ToolDef describes one tool exposed by the server.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type toolDefWire struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type toolsListResult struct {
	Tools      []toolDefWire `json:"tools"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ListTools issues tools/list, transparently paginating via nextCursor
// until the server stops returning one.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	var all []ToolDef
	cursor := ""
	for {
		raw, err := c.call(ctx, "tools/list", toolsListParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}
		var res toolsListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("mcp: decode tools/list result: %w", err)
		}
		for _, t := range res.Tools {
			all = append(all, ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

// ToolResult is the outcome of a CallTool invocation.
type ToolResult struct {
	Text    string // concatenated text content parts
	IsError bool
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolsCallResult struct {
	Content []contentPart `json:"content"`
	IsError bool          `json:"isError"`
}

// CallTool issues tools/call with the given arguments (a JSON object; a nil
// args is sent as {}). Content parts of type "text" are concatenated in
// order into ToolResult.Text; other content types (e.g. images) are ignored
// in v1.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	raw, err := c.call(ctx, "tools/call", toolsCallParams{Name: name, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("mcp: tools/call: %w", err)
	}
	var res toolsCallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: decode tools/call result: %w", err)
	}
	var sb strings.Builder
	for _, p := range res.Content {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return &ToolResult{Text: sb.String(), IsError: res.IsError}, nil
}
