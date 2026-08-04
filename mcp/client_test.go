package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeTransport is an in-memory, channel-backed Transport used to script a
// fake MCP server for tests without touching a real subprocess.
type pipeTransport struct {
	send      chan json.RawMessage // messages written by this side
	recv      chan json.RawMessage // messages read by this side
	closed    chan struct{}
	closeOnce sync.Once
}

func newPipePair() (client, server *pipeTransport) {
	c2s := make(chan json.RawMessage, 64)
	s2c := make(chan json.RawMessage, 64)
	client = &pipeTransport{send: c2s, recv: s2c, closed: make(chan struct{})}
	server = &pipeTransport{send: s2c, recv: c2s, closed: make(chan struct{})}
	return client, server
}

func (t *pipeTransport) Send(ctx context.Context, msg json.RawMessage) error {
	select {
	case t.send <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.closed:
		return errors.New("pipeTransport: closed")
	}
}

func (t *pipeTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case m := <-t.recv:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, errors.New("pipeTransport: closed")
	}
}

func (t *pipeTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}

// rawMsg is a minimal request envelope for reading what the client sent.
type rawMsg struct {
	ID     *int64          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

const testTimeout = 5 * time.Second

func recvRequest(t *testing.T, server *pipeTransport) rawMsg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	raw, err := server.Receive(ctx)
	if err != nil {
		t.Fatalf("server receive: %v", err)
	}
	var m rawMsg
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("server decode: %v", err)
	}
	return m
}

func sendResult(t *testing.T, server *pipeTransport, id int64, result any) {
	t.Helper()
	resp := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  any    `json:"result"`
	}{"2.0", id, result}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.Send(ctx, mustMarshal(t, resp)); err != nil {
		t.Fatalf("server send: %v", err)
	}
}

func sendError(t *testing.T, server *pipeTransport, id int64, code int, message string) {
	t.Helper()
	resp := struct {
		JSONRPC string      `json:"jsonrpc"`
		ID      int64       `json:"id"`
		Error   rpcErrorObj `json:"error"`
	}{"2.0", id, rpcErrorObj{Code: code, Message: message}}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := server.Send(ctx, mustMarshal(t, resp)); err != nil {
		t.Fatalf("server send: %v", err)
	}
}

func TestInitializeHandshake(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	if req.Method != "initialize" {
		t.Fatalf("method = %q, want initialize", req.Method)
	}
	if req.ID == nil {
		t.Fatalf("initialize request had no id")
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q", params.ProtocolVersion)
	}
	if params.ClientInfo.Name != "go-ai-sdk" || params.ClientInfo.Version != "0.1" {
		t.Errorf("clientInfo = %+v", params.ClientInfo)
	}

	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})

	// After the response, the client must send notifications/initialized.
	notif := recvRequest(t, server)
	if notif.Method != "notifications/initialized" {
		t.Fatalf("method = %q, want notifications/initialized", notif.Method)
	}
	if notif.ID != nil {
		t.Fatalf("notification carried an id: %v", *notif.ID)
	}

	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestInitializeRejectsUnsupportedProtocolVersion(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
	})

	err := <-done
	if err == nil {
		t.Fatal("Initialize: want error for unsupported protocol version, got nil")
	}
	if !strings.Contains(err.Error(), `unsupported protocol version "2024-11-05"`) {
		t.Fatalf("err = %v, want it to name the rejected version", err)
	}

	// The client must not proceed to notifications/initialized after
	// rejecting the version.
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := server.Receive(shortCtx); err == nil {
		t.Fatal("server received a further message after version mismatch, want none")
	}
}

// TestInitializeAcceptsLatestProtocolVersion pins that the client sends the
// latest supported protocol version (2025-06-18, needed to make elicitation
// reachable at all — see supportedProtocolVersions) and, when the server
// echoes that same version back, accepts it and stores it as negotiated.
func TestInitializeAcceptsLatestProtocolVersion(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.ProtocolVersion != "2025-06-18" {
		t.Fatalf("protocolVersion = %q, want 2025-06-18", params.ProtocolVersion)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server) // notifications/initialized

	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := c.ProtocolVersion(); got != "2025-06-18" {
		t.Fatalf("ProtocolVersion() = %q, want 2025-06-18", got)
	}
}

// TestInitializeAcceptsOlderSupportedProtocolVersion pins the back-compat
// half of the negotiation: a server that only understands 2025-03-26 (older
// than what the client requests) is still accepted, not rejected, because
// 2025-03-26 remains in supportedProtocolVersions.
func TestInitializeAcceptsOlderSupportedProtocolVersion(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server) // notifications/initialized

	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := c.ProtocolVersion(); got != "2025-03-26" {
		t.Fatalf("ProtocolVersion() = %q, want 2025-03-26", got)
	}
}

func TestListToolsPagination(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	type toolWire struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

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

	// Page 1: cursor absent, returns 2 tools + nextCursor.
	req := recvRequest(t, server)
	if req.Method != "tools/list" {
		t.Fatalf("method = %q", req.Method)
	}
	var p1 struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req.Params, &p1)
	if p1.Cursor != "" {
		t.Fatalf("first page cursor = %q, want empty", p1.Cursor)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"tools": []toolWire{
			{Name: "alpha", Description: "first tool", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "beta", Description: "second tool", InputSchema: json.RawMessage(`{}`)},
		},
		"nextCursor": "page2",
	})

	// Page 2: cursor "page2", returns 1 tool, no nextCursor.
	req2 := recvRequest(t, server)
	var p2 struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(req2.Params, &p2)
	if p2.Cursor != "page2" {
		t.Fatalf("second page cursor = %q, want page2", p2.Cursor)
	}
	sendResult(t, server, *req2.ID, map[string]any{
		"tools": []toolWire{
			{Name: "gamma", Description: "third tool", InputSchema: json.RawMessage(`{}`)},
		},
	})

	res := <-results
	if res.err != nil {
		t.Fatalf("ListTools: %v", res.err)
	}
	if len(res.tools) != 3 {
		t.Fatalf("got %d tools, want 3: %+v", len(res.tools), res.tools)
	}
	names := []string{res.tools[0].Name, res.tools[1].Name, res.tools[2].Name}
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("tools[%d].Name = %q, want %q", i, names[i], want[i])
		}
	}
}

// TestPaginateRepeatedCursorErrors pins fix #7: a fetch function that keeps
// returning the same non-empty nextCursor (a buggy or malicious server)
// must not loop forever — paginate detects no progress and returns an
// error instead.
func TestPaginateRepeatedCursorErrors(t *testing.T) {
	calls := 0
	_, err := paginate(func(cursor string) ([]int, string, error) {
		calls++
		if calls > 3 {
			t.Fatalf("fetch called %d times, want paginate to stop after detecting the repeated cursor", calls)
		}
		return []int{calls}, "same-cursor", nil
	})
	if err == nil {
		t.Fatal("paginate: want error for a repeated cursor, got nil")
	}
}

// TestPaginateStopsOnEmptyCursor is the normal-termination companion to the
// repeated-cursor test: distinct cursors, then an empty one, must complete
// normally without tripping the repeat-cursor guard.
func TestPaginateStopsOnEmptyCursor(t *testing.T) {
	pages := []string{"", "p1", "p2"}
	i := 0
	items, err := paginate(func(cursor string) ([]int, string, error) {
		if cursor != pages[i] {
			t.Fatalf("call %d: cursor = %q, want %q", i, cursor, pages[i])
		}
		i++
		next := ""
		if i < len(pages) {
			next = pages[i]
		}
		return []int{i}, next, nil
	})
	if err != nil {
		t.Fatalf("paginate: %v", err)
	}
	if len(items) != len(pages) {
		t.Fatalf("got %d items, want %d", len(items), len(pages))
	}
}

// TestListToolsRequiresCapability and TestCallToolRequiresCapability pin
// fix #8: tools/list and tools/call must be gated on the "tools" server
// capability the same way resources/prompts/completions already are,
// returning *CapabilityError without sending any request.
func TestListToolsRequiresCapability(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()
	// serverCaps left nil: as if Initialize's server never advertised "tools".

	_, err := c.ListTools(context.Background())
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityError", err)
	}
	if capErr.Capability != "tools" {
		t.Fatalf("capErr.Capability = %q, want tools", capErr.Capability)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := server.Receive(shortCtx); err == nil {
		t.Fatal("server received a request despite the missing capability, want none")
	}
}

func TestCallToolRequiresCapability(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	_, err := c.CallTool(context.Background(), "echo", nil)
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityError", err)
	}
	if capErr.Capability != "tools" {
		t.Fatalf("capErr.Capability = %q, want tools", capErr.Capability)
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := server.Receive(shortCtx); err == nil {
		t.Fatal("server received a request despite the missing capability, want none")
	}
}

func TestCallToolConcatenatesTextAndIgnoresOtherTypes(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	type result struct {
		res *ToolResult
		err error
	}
	results := make(chan result, 1)
	go func() {
		r, err := c.CallTool(context.Background(), "search", json.RawMessage(`{"q":"hi"}`))
		results <- result{r, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "tools/call" {
		t.Fatalf("method = %q", req.Method)
	}
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "search" {
		t.Fatalf("name = %q", params.Name)
	}

	sendResult(t, server, *req.ID, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": "hello "},
			{"type": "image", "data": "base64stuff"},
			{"type": "text", "text": "world"},
		},
		"isError": false,
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("CallTool: %v", r.err)
	}
	if r.res.Text != "hello world" {
		t.Fatalf("Text = %q, want %q", r.res.Text, "hello world")
	}
	if r.res.IsError {
		t.Fatalf("IsError = true, want false")
	}
}

func TestCallToolIsError(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	type result struct {
		res *ToolResult
		err error
	}
	results := make(chan result, 1)
	go func() {
		r, err := c.CallTool(context.Background(), "boom", nil)
		results <- result{r, err}
	}()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "it broke"}},
		"isError": true,
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("CallTool: %v", r.err)
	}
	if !r.res.IsError {
		t.Fatalf("IsError = false, want true")
	}
	if r.res.Text != "it broke" {
		t.Fatalf("Text = %q", r.res.Text)
	}
}

func TestRPCErrorSurfaces(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	results := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "missing", nil)
		results <- err
	}()

	req := recvRequest(t, server)
	sendError(t, server, *req.ID, -32601, "method not found")

	err := <-results
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v, want *RPCError", err)
	}
	if rpcErr.Code != -32601 || rpcErr.Message != "method not found" {
		t.Fatalf("rpcErr = %+v", rpcErr)
	}
}

func TestContextCancellationAbandonsCall(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "slow", nil)
		results <- err
	}()

	// Let the request go out, then cancel before any response arrives.
	recvRequest(t, server)
	cancel()

	select {
	case err := <-results:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("CallTool did not return after ctx cancellation")
	}

	// The pending entry must have been removed so a later, unrelated
	// response for the same id (if the id were ever reused) can't leak in;
	// more directly, the pending map should now be empty.
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending map has %d entries after cancellation, want 0", n)
	}
}

func TestCloseUnblocksPendingCalls(t *testing.T) {
	c, server := withCap("tools")

	results := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "never-answered", nil)
		results <- err
	}()

	recvRequest(t, server) // let the request go out; server never answers

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-results:
		if err == nil {
			t.Fatal("want error after Close, got nil")
		}
		if !strings.Contains(err.Error(), errClosedMsg) {
			t.Fatalf("err = %q, want to contain %q", err.Error(), errClosedMsg)
		}
	case <-time.After(testTimeout):
		t.Fatal("CallTool did not unblock after Close")
	}
}

func TestUnknownIDIsDroppedSilently(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	// Server sends a response for an id nobody is waiting on; must not
	// panic or wedge the client. Then a real call still works.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		_ = server.Send(ctx, mustMarshal(t, map[string]any{
			"jsonrpc": "2.0", "id": 9999, "result": map[string]any{},
		}))
	}()

	time.Sleep(20 * time.Millisecond) // give recvLoop a chance to process it

	results := make(chan error, 1)
	go func() {
		err := c.Initialize(context.Background())
		results <- err
	}()

	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{"protocolVersion": "2025-03-26"})
	recvRequest(t, server) // notifications/initialized

	if err := <-results; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}
