package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// sendServerRequest sends a server-initiated JSON-RPC request (id + method)
// from the fake server to the client.
func sendServerRequest(t *testing.T, server *pipeTransport, id int64, method string, params any) {
	t.Helper()
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", id, method, params}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.Send(ctx, mustMarshal(t, req)); err != nil {
		t.Fatalf("server send: %v", err)
	}
}

// serverResponseMsg is a minimal envelope for reading what the client sent
// back in reply to a server-initiated request.
type serverResponseMsg struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcErrorObj    `json:"error"`
}

func recvServerResponse(t *testing.T, server *pipeTransport) serverResponseMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	raw, err := server.Receive(ctx)
	if err != nil {
		t.Fatalf("server receive: %v", err)
	}
	var m serverResponseMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("server decode: %v", err)
	}
	return m
}

func TestElicitationHandlerAccept(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	var gotReq ElicitationRequest
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		gotReq = req
		return ElicitationResult{Action: "accept", Content: map[string]any{"name": "Al"}}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 42, "elicitation/create", map[string]any{
		"message":         "Please provide your name",
		"requestedSchema": map[string]any{"type": "object"},
	})

	resp := recvServerResponse(t, server)
	if resp.ID != 42 {
		t.Fatalf("ID = %d, want 42", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("Error = %+v, want nil", resp.Error)
	}
	var result struct {
		Action  string         `json:"action"`
		Content map[string]any `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Action != "accept" {
		t.Fatalf("Action = %q, want accept", result.Action)
	}
	if result.Content["name"] != "Al" {
		t.Fatalf("Content = %+v", result.Content)
	}
	if gotReq.Message != "Please provide your name" {
		t.Fatalf("handler got Message = %q", gotReq.Message)
	}
}

func TestElicitationNilHandlerAutoDeclines(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 7, "elicitation/create", map[string]any{
		"message": "input?",
	})

	resp := recvServerResponse(t, server)
	if resp.ID != 7 {
		t.Fatalf("ID = %d, want 7", resp.ID)
	}
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Action != "decline" {
		t.Fatalf("Action = %q, want decline", result.Action)
	}
}

func TestElicitationHandlerErrorRespondsCancel(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		return ElicitationResult{}, errors.New("boom")
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	sendServerRequest(t, server, 3, "elicitation/create", map[string]any{
		"message": "input?",
	})

	resp := recvServerResponse(t, server)
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Action != "cancel" {
		t.Fatalf("Action = %q, want cancel", result.Action)
	}
}

func TestUnknownServerMethodRespondsMethodNotFound(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 5, "totally/unknown", map[string]any{})

	resp := recvServerResponse(t, server)
	if resp.ID != 5 {
		t.Fatalf("ID = %d, want 5", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32601")
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("Error.Code = %d, want -32601", resp.Error.Code)
	}
}

func TestInitializeDeclaresElicitationOnlyWithHandler(t *testing.T) {
	// Without a handler: capabilities must not include "elicitation".
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if _, ok := params.Capabilities["elicitation"]; ok {
		t.Fatalf("capabilities declared elicitation without a handler: %+v", params.Capabilities)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server)
	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestInitializeDeclaresElicitationWithHandler(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		return ElicitationResult{Action: "decline"}, nil
	})

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if _, ok := params.Capabilities["elicitation"]; !ok {
		t.Fatalf("capabilities did not declare elicitation with a handler set: %+v", params.Capabilities)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server)
	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// TestServerRequestConcurrentWithClientCall exercises the race the brief
// calls out explicitly: a server-initiated request arrives while a client
// call is in flight. Both must be served correctly and responses must not
// cross-wire. Run with -race.
func TestServerRequestConcurrentWithClientCall(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	handlerCalled := make(chan struct{}, 1)
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		handlerCalled <- struct{}{}
		return ElicitationResult{Action: "accept", Content: map[string]any{"ok": true}}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"tools": map[string]any{}})

	// Start a client call that will remain in-flight.
	callResults := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "slow-tool", nil)
		callResults <- err
	}()
	callReq := recvRequest(t, server)
	if callReq.Method != "tools/call" {
		t.Fatalf("method = %q, want tools/call", callReq.Method)
	}

	// While the call is in flight, the server sends an elicitation request.
	sendServerRequest(t, server, 999, "elicitation/create", map[string]any{
		"message": "need input",
	})

	select {
	case <-handlerCalled:
	case <-time.After(testTimeout):
		t.Fatal("elicitation handler was not invoked")
	}

	resp := recvServerResponse(t, server)
	if resp.ID != 999 {
		t.Fatalf("elicitation response ID = %d, want 999", resp.ID)
	}
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Action != "accept" {
		t.Fatalf("Action = %q, want accept", result.Action)
	}

	// Now answer the original call; it must still resolve correctly, proving
	// the elicitation dispatch did not disturb pending-call bookkeeping.
	sendResult(t, server, *callReq.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "done"}},
		"isError": false,
	})

	if err := <-callResults; err != nil {
		t.Fatalf("CallTool: %v", err)
	}
}
