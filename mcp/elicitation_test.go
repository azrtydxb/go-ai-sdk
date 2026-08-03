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

// sendServerRequestRawID sends a server-initiated JSON-RPC request whose id
// is an arbitrary raw JSON value (e.g. a JSON-RPC-legal string id), to pin
// that string ids round-trip through dispatchServerRequest instead of being
// silently dropped (a *int64-typed id field would fail to unmarshal a
// string id, and recvLoop's "malformed message, drop" path would eat the
// entire request).
func sendServerRequestRawID(t *testing.T, server *pipeTransport, rawID json.RawMessage, method string, params any) {
	t.Helper()
	req := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  any             `json:"params,omitempty"`
	}{"2.0", rawID, method, params}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.Send(ctx, mustMarshal(t, req)); err != nil {
		t.Fatalf("server send: %v", err)
	}
}

// recvServerResponseRawID is like recvServerResponse but preserves the raw
// id bytes instead of decoding into an int64, so a string id can be
// asserted against exactly.
func recvServerResponseRawID(t *testing.T, server *pipeTransport) (rawID json.RawMessage, result json.RawMessage, errObj *rpcErrorObj) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	raw, err := server.Receive(ctx)
	if err != nil {
		t.Fatalf("server receive: %v", err)
	}
	var m struct {
		ID     json.RawMessage `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcErrorObj    `json:"error"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("server decode: %v", err)
	}
	return m.ID, m.Result, m.Error
}

// TestServerRequestStringIDRoundTrips pins that a server-initiated request
// with a JSON-RPC-legal *string* id (not the int64 ids this client
// generates for its own outgoing calls) is dispatched and answered with
// that exact string id echoed back — not dropped, and not coerced/truncated
// to a number.
func TestServerRequestStringIDRoundTrips(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		return ElicitationResult{Action: "decline"}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	sendServerRequestRawID(t, server, json.RawMessage(`"req-1"`), "elicitation/create", map[string]any{
		"message": "input?",
	})

	gotID, result, errObj := recvServerResponseRawID(t, server)
	if errObj != nil {
		t.Fatalf("Error = %+v, want nil", errObj)
	}
	if string(gotID) != `"req-1"` {
		t.Fatalf("ID = %s, want %q", gotID, `"req-1"`)
	}
	var res struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Action != "decline" {
		t.Fatalf("Action = %q, want decline", res.Action)
	}
}

// TestElicitationCreateMalformedParamsRespondsInvalidParams pins that
// unparseable "elicitation/create" params get a JSON-RPC -32602 "Invalid
// params" error reply (mirroring the -32601 "Method not found" path for
// unknown methods), not a synthesized Action "cancel" result — a malformed
// request is a protocol-level error, not a user decision the caller made.
func TestElicitationCreateMalformedParamsRespondsInvalidParams(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		t.Fatal("handler should not be invoked for malformed params")
		return ElicitationResult{}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	// "params" is a JSON array where an object is expected: unmarshal into
	// elicitationCreateParamsWire fails.
	sendServerRequest(t, server, 11, "elicitation/create", []int{1, 2, 3})

	resp := recvServerResponse(t, server)
	if resp.ID != 11 {
		t.Fatalf("ID = %d, want 11", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32602")
	}
	if resp.Error.Code != rpcInvalidParams {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, rpcInvalidParams)
	}
	if resp.Result != nil {
		t.Fatalf("Result = %s, want nil (error reply must not also carry a result)", resp.Result)
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
