package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Resource describes one resource exposed by the server.
type Resource struct {
	URI         string
	Name        string
	Title       string
	Description string
	MimeType    string
}

// ResourceTemplate describes one RFC 6570 URI template exposed by the
// server, from which concrete resource URIs can be constructed.
type ResourceTemplate struct {
	URITemplate string
	Name        string
	Title       string
	Description string
	MimeType    string
}

// ResourceContents is the body of one resource, as returned by ReadResource
// or embedded in a prompt message. Exactly one of Text or Blob is set,
// depending on whether the server sent "text" or a base64 "blob".
type ResourceContents struct {
	URI      string
	MimeType string
	Text     string
	Blob     []byte
}

type resourceWire struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

type resourcesListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type resourcesListResult struct {
	Resources  []resourceWire `json:"resources"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

// ListResources issues resources/list, transparently paginating via
// nextCursor until the server stops returning one. It returns a
// *CapabilityError without sending any request if the server's "initialize"
// response did not advertise the "resources" capability.
func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if !c.hasCapability("resources") {
		return nil, &CapabilityError{Capability: "resources"}
	}
	return paginate(func(cursor string) ([]Resource, string, error) {
		raw, err := c.call(ctx, "resources/list", resourcesListParams{Cursor: cursor})
		if err != nil {
			return nil, "", fmt.Errorf("mcp: resources/list: %w", err)
		}
		var res resourcesListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, "", fmt.Errorf("mcp: decode resources/list result: %w", err)
		}
		items := make([]Resource, len(res.Resources))
		for i, r := range res.Resources {
			items[i] = Resource{URI: r.URI, Name: r.Name, Title: r.Title, Description: r.Description, MimeType: r.MimeType}
		}
		return items, res.NextCursor, nil
	})
}

type resourceTemplateWire struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

type resourceTemplatesListResult struct {
	ResourceTemplates []resourceTemplateWire `json:"resourceTemplates"`
	NextCursor        string                 `json:"nextCursor,omitempty"`
}

// ListResourceTemplates issues resources/templates/list, transparently
// paginating via nextCursor. Same capability gate as ListResources.
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) {
	if !c.hasCapability("resources") {
		return nil, &CapabilityError{Capability: "resources"}
	}
	return paginate(func(cursor string) ([]ResourceTemplate, string, error) {
		raw, err := c.call(ctx, "resources/templates/list", resourcesListParams{Cursor: cursor})
		if err != nil {
			return nil, "", fmt.Errorf("mcp: resources/templates/list: %w", err)
		}
		var res resourceTemplatesListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, "", fmt.Errorf("mcp: decode resources/templates/list result: %w", err)
		}
		items := make([]ResourceTemplate, len(res.ResourceTemplates))
		for i, r := range res.ResourceTemplates {
			items[i] = ResourceTemplate{URITemplate: r.URITemplate, Name: r.Name, Title: r.Title, Description: r.Description, MimeType: r.MimeType}
		}
		return items, res.NextCursor, nil
	})
}

type resourceContentsWire struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// decodeResourceContents converts one wire resourceContentsWire into a
// ResourceContents, base64-decoding Blob when present.
func decodeResourceContents(w resourceContentsWire) (ResourceContents, error) {
	rc := ResourceContents{URI: w.URI, MimeType: w.MimeType, Text: w.Text}
	if w.Blob != "" {
		b, err := base64.StdEncoding.DecodeString(w.Blob)
		if err != nil {
			return ResourceContents{}, fmt.Errorf("mcp: decode resource blob: %w", err)
		}
		rc.Blob = b
	}
	return rc, nil
}

type resourcesReadParams struct {
	URI string `json:"uri"`
}

type resourcesReadResult struct {
	Contents []resourceContentsWire `json:"contents"`
}

// ReadResource issues resources/read for uri. A resource contents entry
// carrying "text" decodes into ResourceContents.Text; one carrying a
// base64 "blob" decodes into ResourceContents.Blob.
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContents, error) {
	if !c.hasCapability("resources") {
		return nil, &CapabilityError{Capability: "resources"}
	}
	raw, err := c.call(ctx, "resources/read", resourcesReadParams{URI: uri})
	if err != nil {
		return nil, fmt.Errorf("mcp: resources/read: %w", err)
	}
	var res resourcesReadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: decode resources/read result: %w", err)
	}
	items := make([]ResourceContents, len(res.Contents))
	for i, rcw := range res.Contents {
		rc, err := decodeResourceContents(rcw)
		if err != nil {
			return nil, err
		}
		items[i] = rc
	}
	return items, nil
}
