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
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      clientInfo     `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities"`
}

// initializeResult is the subset of the "initialize" response this client
// inspects: the server's negotiated protocol version and its advertised
// capabilities object (stored verbatim, keyed by capability name).
type initializeResult struct {
	ProtocolVersion string                     `json:"protocolVersion"`
	Capabilities    map[string]json.RawMessage `json:"capabilities"`
}

// Initialize performs the MCP handshake: an "initialize" request, followed
// by a "notifications/initialized" notification once the server replies. It
// rejects the handshake if the server's negotiated protocolVersion isn't
// the one this client speaks (protocolVersion, "2025-03-26"). The server's
// advertised capabilities are stored on the Client so later calls (e.g.
// ListResources, ListPrompts) can gate on them.
func (c *Client) Initialize(ctx context.Context) error {
	caps := map[string]any{}
	c.mu.Lock()
	hasElicitationHandler := c.elicitationHandler != nil
	c.mu.Unlock()
	if hasElicitationHandler {
		caps["elicitation"] = struct{}{}
	}
	params := initializeParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      clientInfo{Name: "go-ai-sdk", Version: "0.1"},
		Capabilities:    caps,
	}
	raw, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	var res initializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp: decode initialize result: %w", err)
	}
	if res.ProtocolVersion != protocolVersion {
		return fmt.Errorf("mcp: server negotiated unsupported protocol version %q", res.ProtocolVersion)
	}
	c.mu.Lock()
	c.serverCaps = res.Capabilities
	c.mu.Unlock()
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: notifications/initialized: %w", err)
	}
	return nil
}

// CapabilityError is returned when a method requires a server capability
// (as advertised in the "initialize" response) that the server did not
// declare. It is returned before any request is sent for that method.
type CapabilityError struct {
	Capability string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("mcp: server does not support capability %q", e.Capability)
}

// hasCapability reports whether the server advertised the named top-level
// capability in its "initialize" response.
func (c *Client) hasCapability(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.serverCaps == nil {
		return false
	}
	_, ok := c.serverCaps[name]
	return ok
}

// paginate drives cursor-based pagination shared by list-style MCP methods:
// fetch is invoked with the cursor for each page ("" for the first) and
// returns that page's items, the nextCursor (empty when there are no more
// pages), or an error. Results are concatenated across pages in order.
func paginate[T any](fetch func(cursor string) (items []T, nextCursor string, err error)) ([]T, error) {
	var all []T
	cursor := ""
	for {
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
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
	return paginate(func(cursor string) ([]ToolDef, string, error) {
		raw, err := c.call(ctx, "tools/list", toolsListParams{Cursor: cursor})
		if err != nil {
			return nil, "", fmt.Errorf("mcp: tools/list: %w", err)
		}
		var res toolsListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, "", fmt.Errorf("mcp: decode tools/list result: %w", err)
		}
		items := make([]ToolDef, len(res.Tools))
		for i, t := range res.Tools {
			items[i] = ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
		}
		return items, res.NextCursor, nil
	})
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
