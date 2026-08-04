package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// supportedProtocolVersions is the ordered set of MCP protocol versions this
// client supports, latest first. The latest ("2025-06-18") is sent as the
// requested version in the "initialize" request; any version in this set
// that the server returns is accepted and stored as the negotiated version
// (see Client.ProtocolVersion). Only a version outside this set is rejected.
//
// Elicitation ("elicitation/create") is a 2025-06-18 feature: a
// spec-conforming server never sends it to a client that negotiated
// 2025-03-26. Pinning the client to 2025-03-26 alone (as an earlier version
// of this client did) would therefore make elicitation permanently
// unreachable even with a handler installed. Supporting the range keeps
// elicitation reachable against a 2025-06-18 server while remaining
// back-compatible with a 2025-03-26 server.
var supportedProtocolVersions = []string{"2025-06-18", "2025-03-26"}

// isSupportedProtocolVersion reports whether v is one of
// supportedProtocolVersions.
func isSupportedProtocolVersion(v string) bool {
	for _, sv := range supportedProtocolVersions {
		if sv == v {
			return true
		}
	}
	return false
}

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
// sends the latest supported protocol version (supportedProtocolVersions[0])
// and accepts any version the server returns that is in
// supportedProtocolVersions, rejecting the handshake only if the returned
// version is outside that set. The negotiated version is stored on the
// Client (see ProtocolVersion). The server's advertised capabilities are
// likewise stored on the Client so later calls (e.g. ListResources,
// ListPrompts) can gate on them.
func (c *Client) Initialize(ctx context.Context) error {
	caps := map[string]any{}
	c.mu.Lock()
	hasElicitationHandler := c.elicitationHandler != nil
	c.mu.Unlock()
	if hasElicitationHandler {
		caps["elicitation"] = struct{}{}
	}
	params := initializeParams{
		ProtocolVersion: supportedProtocolVersions[0],
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
	if !isSupportedProtocolVersion(res.ProtocolVersion) {
		return fmt.Errorf("mcp: server negotiated unsupported protocol version %q", res.ProtocolVersion)
	}
	c.mu.Lock()
	c.serverCaps = res.Capabilities
	c.negotiatedProtocolVersion = res.ProtocolVersion
	c.mu.Unlock()
	if err := c.notify(ctx, "notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp: notifications/initialized: %w", err)
	}
	return nil
}

// ProtocolVersion returns the MCP protocol version negotiated during
// Initialize (one of supportedProtocolVersions). It is empty until
// Initialize has completed successfully.
func (c *Client) ProtocolVersion() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.negotiatedProtocolVersion
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

// maxPaginatePages bounds the number of pages paginate will fetch, as a
// backstop against a misbehaving server that never terminates pagination
// (see paginate's repeat-cursor check for the primary guard).
const maxPaginatePages = 10000

// paginate drives cursor-based pagination shared by list-style MCP methods:
// fetch is invoked with the cursor for each page ("" for the first) and
// returns that page's items, the nextCursor (empty when there are no more
// pages), or an error. Results are concatenated across pages in order.
//
// A server that returns the same nextCursor twice in a row (buggy, or
// deliberately malicious) would otherwise make this loop forever making
// identical requests; paginate detects that (next == cursor, with cursor
// non-empty) and returns an error instead. maxPaginatePages is a second,
// coarser backstop against a server whose cursor keeps changing but never
// terminates.
func paginate[T any](fetch func(cursor string) (items []T, nextCursor string, err error)) ([]T, error) {
	var all []T
	cursor := ""
	for page := 0; ; page++ {
		if page >= maxPaginatePages {
			return nil, fmt.Errorf("mcp: pagination exceeded %d pages without terminating", maxPaginatePages)
		}
		items, next, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		if next == cursor {
			return nil, fmt.Errorf("mcp: server returned the same cursor %q twice in a row; aborting to avoid looping forever", next)
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
// until the server stops returning one. It returns a *CapabilityError
// without sending any request if the server's "initialize" response didn't
// advertise the "tools" capability.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	if !c.hasCapability("tools") {
		return nil, &CapabilityError{Capability: "tools"}
	}
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
// in v1. It returns a *CapabilityError without sending any request if the
// server's "initialize" response didn't advertise the "tools" capability.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error) {
	if !c.hasCapability("tools") {
		return nil, &CapabilityError{Capability: "tools"}
	}
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
