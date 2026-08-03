package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestListPromptsWithArguments(t *testing.T) {
	c, server := withCap("prompts")
	defer c.Close()

	type outcome struct {
		res []Prompt
		err error
	}
	results := make(chan outcome, 1)
	go func() {
		res, err := c.ListPrompts(context.Background())
		results <- outcome{res, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "prompts/list" {
		t.Fatalf("method = %q, want prompts/list", req.Method)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"prompts": []map[string]any{
			{
				"name":        "greet",
				"description": "Say hello",
				"arguments": []map[string]any{
					{"name": "name", "description": "who to greet", "required": true},
					{"name": "formal"},
				},
			},
		},
		"nextCursor": "page2",
	})

	req2 := recvRequest(t, server)
	var p2 struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req2.Params, &p2)
	if p2.Cursor != "page2" {
		t.Fatalf("cursor = %q, want page2", p2.Cursor)
	}
	sendResult(t, server, *req2.ID, map[string]any{
		"prompts": []map[string]any{{"name": "farewell"}},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("ListPrompts: %v", r.err)
	}
	if len(r.res) != 2 {
		t.Fatalf("got %d prompts, want 2: %+v", len(r.res), r.res)
	}
	p := r.res[0]
	if p.Name != "greet" || p.Description != "Say hello" {
		t.Errorf("prompts[0] = %+v", p)
	}
	if len(p.Arguments) != 2 {
		t.Fatalf("got %d arguments, want 2", len(p.Arguments))
	}
	if p.Arguments[0].Name != "name" || !p.Arguments[0].Required {
		t.Errorf("arguments[0] = %+v", p.Arguments[0])
	}
	if p.Arguments[1].Name != "formal" || p.Arguments[1].Required {
		t.Errorf("arguments[1] = %+v", p.Arguments[1])
	}
	if r.res[1].Name != "farewell" {
		t.Errorf("prompts[1].Name = %q, want farewell", r.res[1].Name)
	}
}

func TestGetPromptMultiPartWithEmbeddedResource(t *testing.T) {
	c, server := withCap("prompts")
	defer c.Close()

	type outcome struct {
		description string
		messages    []PromptMessage
		err         error
	}
	results := make(chan outcome, 1)
	go func() {
		desc, msgs, err := c.GetPrompt(context.Background(), "greet", map[string]string{"name": "Ada"})
		results <- outcome{desc, msgs, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "prompts/get" {
		t.Fatalf("method = %q, want prompts/get", req.Method)
	}
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "greet" || params.Arguments["name"] != "Ada" {
		t.Fatalf("params = %+v", params)
	}

	blob := base64.StdEncoding.EncodeToString([]byte("resource-bytes"))
	sendResult(t, server, *req.ID, map[string]any{
		"description": "Greets Ada",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "Hello, Ada!"},
					{
						"type": "resource",
						"resource": map[string]any{
							"uri":      "file:///greeting.bin",
							"mimeType": "application/octet-stream",
							"blob":     blob,
						},
					},
				},
			},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("GetPrompt: %v", r.err)
	}
	if r.description != "Greets Ada" {
		t.Fatalf("description = %q, want Greets Ada", r.description)
	}
	if len(r.messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(r.messages))
	}
	msg := r.messages[0]
	if msg.Role != "user" {
		t.Errorf("role = %q, want user", msg.Role)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("got %d content parts, want 2: %+v", len(msg.Content), msg.Content)
	}
	if msg.Content[0].Type != "text" || msg.Content[0].Text != "Hello, Ada!" {
		t.Errorf("content[0] = %+v", msg.Content[0])
	}
	res := msg.Content[1]
	if res.Type != "resource" || res.Resource == nil {
		t.Fatalf("content[1] = %+v, want resource with embedded contents", res)
	}
	if res.Resource.URI != "file:///greeting.bin" {
		t.Errorf("resource.URI = %q", res.Resource.URI)
	}
	if string(res.Resource.Blob) != "resource-bytes" {
		t.Errorf("resource.Blob = %q, want resource-bytes", res.Resource.Blob)
	}
}

func TestGetPromptSingleContentObject(t *testing.T) {
	// Per the MCP spec, "content" on a message is a single object, not an
	// array. The client must flatten this into a one-element PromptPart
	// slice rather than erroring.
	c, server := withCap("prompts")
	defer c.Close()

	type outcome struct {
		messages []PromptMessage
		err      error
	}
	results := make(chan outcome, 1)
	go func() {
		_, msgs, err := c.GetPrompt(context.Background(), "greet", nil)
		results <- outcome{msgs, err}
	}()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"messages": []map[string]any{
			{
				"role":    "assistant",
				"content": map[string]any{"type": "text", "text": "hi"},
			},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("GetPrompt: %v", r.err)
	}
	if len(r.messages) != 1 || len(r.messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", r.messages)
	}
	if r.messages[0].Content[0].Text != "hi" {
		t.Errorf("content[0].Text = %q, want hi", r.messages[0].Content[0].Text)
	}
}

func TestGetPromptUnknownContentTypePreserved(t *testing.T) {
	c, server := withCap("prompts")
	defer c.Close()

	type outcome struct {
		messages []PromptMessage
		err      error
	}
	results := make(chan outcome, 1)
	go func() {
		_, msgs, err := c.GetPrompt(context.Background(), "greet", nil)
		results <- outcome{msgs, err}
	}()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "video", "data": "irrelevant"},
				},
			},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("GetPrompt: %v", r.err)
	}
	if len(r.messages) != 1 || len(r.messages[0].Content) != 1 {
		t.Fatalf("messages = %+v", r.messages)
	}
	part := r.messages[0].Content[0]
	if part.Type != "video" {
		t.Errorf("Type = %q, want video", part.Type)
	}
	if part.Text != "" || part.Data != nil {
		t.Errorf("part = %+v, want empty Text/Data for unknown type", part)
	}
}

func TestPromptsCapabilityAbsent(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	_, _, err := c.GetPrompt(context.Background(), "greet", nil)
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityError", err)
	}
	if capErr.Capability != "prompts" {
		t.Fatalf("capErr.Capability = %q, want prompts", capErr.Capability)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := server.Receive(shortCtx); err == nil {
		t.Fatal("server received a message, want none")
	}
}

func TestGetPromptRPCErrorSurfaces(t *testing.T) {
	c, server := withCap("prompts")
	defer c.Close()

	results := make(chan error, 1)
	go func() {
		_, _, err := c.GetPrompt(context.Background(), "missing", nil)
		results <- err
	}()

	req := recvRequest(t, server)
	sendError(t, server, *req.ID, -32602, "prompt not found")

	err := <-results
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v, want *RPCError", err)
	}
	if rpcErr.Code != -32602 {
		t.Fatalf("rpcErr.Code = %d, want -32602", rpcErr.Code)
	}
}
