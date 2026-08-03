package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Prompt describes one prompt template exposed by the server.
type Prompt struct {
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgument
}

// PromptArgument describes one argument a Prompt accepts.
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// PromptMessage is one message in a prompt's rendered conversation.
type PromptMessage struct {
	Role    string // "user" / "assistant"
	Content []PromptPart
}

// PromptPart is one content part of a PromptMessage. Type is always set;
// which of Text, Resource, or Data is populated depends on Type ("text",
// "resource", "image", "audio"). Content types the client doesn't recognize
// are preserved with Type set and no error.
type PromptPart struct {
	Type     string
	Text     string
	Resource *ResourceContents // for "resource" parts
	Data     []byte            // decoded, for "image"/"audio" parts
	MimeType string
}

type promptArgumentWire struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type promptWire struct {
	Name        string               `json:"name"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Arguments   []promptArgumentWire `json:"arguments"`
}

type promptsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type promptsListResult struct {
	Prompts    []promptWire `json:"prompts"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// ListPrompts issues prompts/list, transparently paginating via
// nextCursor. It returns a *CapabilityError without sending any request if
// the server's "initialize" response did not advertise the "prompts"
// capability.
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	if !c.hasCapability("prompts") {
		return nil, &CapabilityError{Capability: "prompts"}
	}
	return paginate(func(cursor string) ([]Prompt, string, error) {
		raw, err := c.call(ctx, "prompts/list", promptsListParams{Cursor: cursor})
		if err != nil {
			return nil, "", fmt.Errorf("mcp: prompts/list: %w", err)
		}
		var res promptsListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, "", fmt.Errorf("mcp: decode prompts/list result: %w", err)
		}
		items := make([]Prompt, len(res.Prompts))
		for i, p := range res.Prompts {
			args := make([]PromptArgument, len(p.Arguments))
			for j, a := range p.Arguments {
				args[j] = PromptArgument{Name: a.Name, Description: a.Description, Required: a.Required}
			}
			items[i] = Prompt{Name: p.Name, Title: p.Title, Description: p.Description, Arguments: args}
		}
		return items, res.NextCursor, nil
	})
}

// promptContentWire is the wire shape of one prompt message content part:
// TextContent, ImageContent, AudioContent, or EmbeddedResource. Fields not
// applicable to a given Type are simply absent on the wire.
type promptContentWire struct {
	Type     string                `json:"type"`
	Text     string                `json:"text,omitempty"`
	Data     string                `json:"data,omitempty"`
	MimeType string                `json:"mimeType,omitempty"`
	Resource *resourceContentsWire `json:"resource,omitempty"`
}

// decodePromptPart converts one wire content part into a PromptPart.
// Unknown Type values are preserved (Type set) with no error, per the MCP
// convention of forward-compatible content types.
func decodePromptPart(w promptContentWire) (PromptPart, error) {
	part := PromptPart{Type: w.Type, MimeType: w.MimeType}
	switch w.Type {
	case "text":
		part.Text = w.Text
	case "resource":
		if w.Resource != nil {
			rc, err := decodeResourceContents(*w.Resource)
			if err != nil {
				return PromptPart{}, err
			}
			part.Resource = &rc
		}
	case "image", "audio":
		if w.Data != "" {
			b, err := base64.StdEncoding.DecodeString(w.Data)
			if err != nil {
				return PromptPart{}, fmt.Errorf("mcp: decode prompt %s data: %w", w.Type, err)
			}
			part.Data = b
		}
	}
	return part, nil
}

// decodePromptContent decodes a message's "content" field, which per the
// MCP spec is a single content object but is accepted here as either a
// single object or an array of them, always flattened into a []PromptPart.
func decodePromptContent(raw json.RawMessage) ([]PromptPart, error) {
	var arr []promptContentWire
	if err := json.Unmarshal(raw, &arr); err == nil {
		parts := make([]PromptPart, len(arr))
		for i, w := range arr {
			p, err := decodePromptPart(w)
			if err != nil {
				return nil, err
			}
			parts[i] = p
		}
		return parts, nil
	}
	var single promptContentWire
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, fmt.Errorf("mcp: decode prompt message content: %w", err)
	}
	p, err := decodePromptPart(single)
	if err != nil {
		return nil, err
	}
	return []PromptPart{p}, nil
}

type promptMessageWire struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type promptsGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

type promptsGetResult struct {
	Description string              `json:"description"`
	Messages    []promptMessageWire `json:"messages"`
}

// GetPrompt issues prompts/get for name with the given arguments (may be
// nil), returning the server's description and the rendered messages with
// content parts flattened into PromptPart slices.
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (string, []PromptMessage, error) {
	if !c.hasCapability("prompts") {
		return "", nil, &CapabilityError{Capability: "prompts"}
	}
	raw, err := c.call(ctx, "prompts/get", promptsGetParams{Name: name, Arguments: args})
	if err != nil {
		return "", nil, fmt.Errorf("mcp: prompts/get: %w", err)
	}
	var res promptsGetResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", nil, fmt.Errorf("mcp: decode prompts/get result: %w", err)
	}
	messages := make([]PromptMessage, len(res.Messages))
	for i, m := range res.Messages {
		parts, err := decodePromptContent(m.Content)
		if err != nil {
			return "", nil, err
		}
		messages[i] = PromptMessage{Role: m.Role, Content: parts}
	}
	return res.Description, messages, nil
}
