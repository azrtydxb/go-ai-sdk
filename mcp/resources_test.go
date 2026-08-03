package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// withCap returns a Client whose serverCaps advertises the given
// capability names (as if Initialize had already run), wired to an
// in-memory fake server.
func withCap(caps ...string) (c *Client, server *pipeTransport) {
	client, server := newPipePair()
	c = NewClient(client)
	m := make(map[string]json.RawMessage, len(caps))
	for _, name := range caps {
		m[name] = json.RawMessage(`{}`)
	}
	c.serverCaps = m
	return c, server
}

func TestListResourcesPagination(t *testing.T) {
	c, server := withCap("resources")
	defer c.Close()

	type wireResource struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description"`
		MimeType    string `json:"mimeType"`
	}

	type outcome struct {
		res []Resource
		err error
	}
	results := make(chan outcome, 1)
	go func() {
		res, err := c.ListResources(context.Background())
		results <- outcome{res, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "resources/list" {
		t.Fatalf("method = %q, want resources/list", req.Method)
	}
	var p1 struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req.Params, &p1)
	if p1.Cursor != "" {
		t.Fatalf("first page cursor = %q, want empty", p1.Cursor)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"resources": []wireResource{
			{URI: "file:///a", Name: "a", Description: "first"},
		},
		"nextCursor": "page2",
	})

	req2 := recvRequest(t, server)
	var p2 struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req2.Params, &p2)
	if p2.Cursor != "page2" {
		t.Fatalf("second page cursor = %q, want page2", p2.Cursor)
	}
	sendResult(t, server, *req2.ID, map[string]any{
		"resources": []wireResource{
			{URI: "file:///b", Name: "b", MimeType: "text/plain"},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("ListResources: %v", r.err)
	}
	if len(r.res) != 2 {
		t.Fatalf("got %d resources, want 2: %+v", len(r.res), r.res)
	}
	if r.res[0].URI != "file:///a" || r.res[0].Description != "first" {
		t.Errorf("resources[0] = %+v", r.res[0])
	}
	if r.res[1].URI != "file:///b" || r.res[1].MimeType != "text/plain" {
		t.Errorf("resources[1] = %+v", r.res[1])
	}
}

func TestListResourceTemplates(t *testing.T) {
	c, server := withCap("resources")
	defer c.Close()

	type outcome struct {
		res []ResourceTemplate
		err error
	}
	results := make(chan outcome, 1)
	go func() {
		res, err := c.ListResourceTemplates(context.Background())
		results <- outcome{res, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "resources/templates/list" {
		t.Fatalf("method = %q, want resources/templates/list", req.Method)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"resourceTemplates": []map[string]any{
			{"uriTemplate": "file:///{name}.txt", "name": "file", "description": "a file"},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("ListResourceTemplates: %v", r.err)
	}
	if len(r.res) != 1 {
		t.Fatalf("got %d templates, want 1", len(r.res))
	}
	if r.res[0].URITemplate != "file:///{name}.txt" || r.res[0].Name != "file" {
		t.Errorf("templates[0] = %+v", r.res[0])
	}
}

func TestReadResourceTextAndBlob(t *testing.T) {
	c, server := withCap("resources")
	defer c.Close()

	type outcome struct {
		res []ResourceContents
		err error
	}
	results := make(chan outcome, 1)
	go func() {
		res, err := c.ReadResource(context.Background(), "file:///a")
		results <- outcome{res, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "resources/read" {
		t.Fatalf("method = %q, want resources/read", req.Method)
	}
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.URI != "file:///a" {
		t.Fatalf("uri = %q, want file:///a", params.URI)
	}

	blob := base64.StdEncoding.EncodeToString([]byte("binary-data"))
	sendResult(t, server, *req.ID, map[string]any{
		"contents": []map[string]any{
			{"uri": "file:///a.txt", "mimeType": "text/plain", "text": "hello"},
			{"uri": "file:///a.bin", "mimeType": "application/octet-stream", "blob": blob},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("ReadResource: %v", r.err)
	}
	if len(r.res) != 2 {
		t.Fatalf("got %d contents, want 2", len(r.res))
	}
	if r.res[0].Text != "hello" {
		t.Errorf("contents[0].Text = %q, want hello", r.res[0].Text)
	}
	if r.res[0].Blob != nil {
		t.Errorf("contents[0].Blob = %v, want nil", r.res[0].Blob)
	}
	if string(r.res[1].Blob) != "binary-data" {
		t.Errorf("contents[1].Blob = %q, want binary-data", r.res[1].Blob)
	}
	if r.res[1].Text != "" {
		t.Errorf("contents[1].Text = %q, want empty", r.res[1].Text)
	}
}

func TestResourcesCapabilityAbsent(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()
	// serverCaps left nil: as if Initialize's server never advertised
	// "resources".

	_, err := c.ListResources(context.Background())
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityError", err)
	}
	if capErr.Capability != "resources" {
		t.Fatalf("capErr.Capability = %q, want resources", capErr.Capability)
	}

	// No request must have been sent to the server.
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := server.Receive(shortCtx); err == nil {
		t.Fatal("server received a message, want none")
	}
}

func TestReadResourceRPCErrorSurfaces(t *testing.T) {
	c, server := withCap("resources")
	defer c.Close()

	results := make(chan error, 1)
	go func() {
		_, err := c.ReadResource(context.Background(), "file:///missing")
		results <- err
	}()

	req := recvRequest(t, server)
	sendError(t, server, *req.ID, -32001, "resource not found")

	err := <-results
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v, want *RPCError", err)
	}
	if rpcErr.Code != -32001 {
		t.Fatalf("rpcErr.Code = %d, want -32001", rpcErr.Code)
	}
}
