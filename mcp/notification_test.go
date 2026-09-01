package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// sendServerNotification sends a JSON-RPC notification (no id) from the fake
// server to the client.
func sendServerNotification(t *testing.T, server *pipeTransport, method string, params any) {
	t.Helper()
	note := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", method, params}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.Send(ctx, mustMarshal(t, note)); err != nil {
		t.Fatalf("server send: %v", err)
	}
}

func TestNotificationHandlerReceivesNotification(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	var (
		mu        sync.Mutex
		gotMethod string
		gotParams json.RawMessage
		done      = make(chan struct{})
	)
	c.SetNotificationHandler(func(method string, params json.RawMessage) {
		mu.Lock()
		gotMethod = method
		gotParams = params
		mu.Unlock()
		close(done)
	})

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerNotification(t, server, "notifications/message", map[string]any{
		"level": "info",
		"data":  "hi",
	})

	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for NotificationHandler to be invoked")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotMethod != "notifications/message" {
		t.Fatalf("method = %q, want notifications/message", gotMethod)
	}
	var params struct {
		Level string `json:"level"`
		Data  string `json:"data"`
	}
	if err := json.Unmarshal(gotParams, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Level != "info" || params.Data != "hi" {
		t.Fatalf("params = %+v", params)
	}
}

// TestNotificationWithoutHandlerDroppedHarmlessly pins that when no
// NotificationHandler is installed, an incoming notification is silently
// dropped (no panic) and the client keeps serving normal calls afterward.
func TestNotificationWithoutHandlerDroppedHarmlessly(t *testing.T) {
	c, server := withCap("tools")
	defer func() { _ = c.Close() }()

	sendServerNotification(t, server, "notifications/message", map[string]any{
		"level": "info",
		"data":  "hi",
	})

	results := make(chan struct {
		tools []ToolDef
		err   error
	}, 1)
	go func() {
		tools, err := c.ListTools(context.Background())
		results <- struct {
			tools []ToolDef
			err   error
		}{tools, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "tools/list" {
		t.Fatalf("method = %q, want tools/list", req.Method)
	}
	sendResult(t, server, *req.ID, map[string]any{"tools": []any{}})

	res := <-results
	if res.err != nil {
		t.Fatalf("ListTools: %v", res.err)
	}
	if len(res.tools) != 0 {
		t.Fatalf("tools = %+v, want empty", res.tools)
	}
}
