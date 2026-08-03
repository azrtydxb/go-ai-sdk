// Package mcp implements a client for the Model Context Protocol (MCP): a
// JSON-RPC 2.0 based protocol that lets an AI application discover and
// invoke tools exposed by an external server process.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// errClosedMsg is the error text used when a call is abandoned because the
// client was closed.
const errClosedMsg = "mcp: client closed"

// rpcRequest is a JSON-RPC 2.0 request object (always carries an id).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcNotification is a JSON-RPC 2.0 notification object (no id, no reply
// expected).
type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcErrorObj is the wire shape of a JSON-RPC error object.
type rpcErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse is the wire shape of anything the server sends us: a response
// to one of our requests (ID set, Method empty), a server-initiated request
// (ID set, Method non-empty), or a notification (ID absent/null).
//
// ID is json.RawMessage rather than *int64 because JSON-RPC 2.0 permits
// string ids, and a spec-conforming server may send a server-initiated
// request (e.g. "elicitation/create") with a string id — a *int64 field
// would fail to unmarshal that message and silently drop it (json.Unmarshal
// returns an error on the whole rpcResponse, and recvLoop's "continue" on
// unmarshal failure means the message — and any reply the server was
// waiting on — vanishes). Responses to our own outgoing calls always carry
// the int64 id we generated (see Client.call), so pending-map lookups
// unmarshal ID into an int64 at match time; server-initiated request ids
// are forwarded verbatim (as raw bytes, whatever their JSON type) in the
// reply so they round-trip exactly as sent.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrorObj    `json:"error,omitempty"`
}

// idIsAbsent reports whether raw represents a missing/null JSON-RPC id
// (i.e. a notification), matching the old *int64-nil semantics for both an
// entirely absent "id" field and an explicit "id": null.
func idIsAbsent(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// serverRequest is a JSON-RPC request sent by the server to the client
// (e.g. "elicitation/create"). Handled by Client.dispatchServerRequest. ID
// is the raw wire id (int or string, per JSON-RPC 2.0) and is echoed back
// verbatim in the reply.
type serverRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// RPCError is returned when the server replies with a JSON-RPC error object.
// It is errors.As-able.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

// Client is a JSON-RPC 2.0 client over a Transport, specialised for MCP.
// A single background goroutine reads incoming messages and dispatches
// responses to the waiting call by id. It is safe for concurrent use.
type Client struct {
	transport Transport

	ctx    context.Context
	cancel context.CancelFunc

	nextID int64

	mu      sync.Mutex
	pending map[int64]chan rpcResponse

	// serverCaps is the raw "capabilities" object from the server's
	// "initialize" response, keyed by top-level capability name (e.g.
	// "resources", "prompts"). Guarded by mu. Nil until Initialize succeeds.
	serverCaps map[string]json.RawMessage

	// elicitationHandler, if set, is invoked for server-initiated
	// "elicitation/create" requests. Guarded by mu.
	elicitationHandler ElicitationHandler

	// sendMu serializes writes to the transport: Transport only guarantees
	// safety for one concurrent writer, but Client.call may be invoked by
	// multiple goroutines at once (concurrent in-flight RPC calls).
	sendMu sync.Mutex

	closeOnce sync.Once
	closeCh   chan struct{}
	closeErr  error

	loopDone chan struct{}
}

// NewClient wraps t. Call Initialize before making any other call.
func NewClient(t Transport) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		transport: t,
		ctx:       ctx,
		cancel:    cancel,
		pending:   make(map[int64]chan rpcResponse),
		closeCh:   make(chan struct{}),
		loopDone:  make(chan struct{}),
	}
	go c.recvLoop()
	return c
}

// recvLoop reads one message at a time and dispatches it. A message with a
// non-nil id and no method is a response to a call we made, matched by id.
// A message with a non-nil id AND a method is a server-initiated request,
// dispatched to dispatchServerRequest. A message with a nil id is a
// notification and is dropped: v1 does not support incoming notifications.
func (c *Client) recvLoop() {
	defer close(c.loopDone)
	for {
		msg, err := c.transport.Receive(c.ctx)
		if err != nil {
			c.closeWith(fmt.Errorf("mcp: transport closed: %w", err))
			return
		}

		var resp rpcResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue // malformed message, drop
		}
		if idIsAbsent(resp.ID) {
			continue // notification, ignored
		}
		if resp.Method != "" {
			// Server-initiated request: dispatch on a new goroutine so a
			// slow handler doesn't block recvLoop from reading further
			// messages (e.g. responses to our own in-flight calls). The id
			// is forwarded verbatim (raw bytes) so string ids round-trip.
			req := serverRequest{ID: resp.ID, Method: resp.Method, Params: resp.Params}
			go c.dispatchServerRequest(req)
			continue
		}

		// A response to one of our own calls: we always generate int64
		// ids for outgoing requests (see Client.call), so the id here is
		// always a JSON number — decode it to find the matching pending
		// entry.
		var idNum int64
		if err := json.Unmarshal(resp.ID, &idNum); err != nil {
			continue // not one of our ids (unexpected shape), drop
		}

		c.mu.Lock()
		ch, ok := c.pending[idNum]
		if ok {
			delete(c.pending, idNum)
		}
		c.mu.Unlock()

		if ok {
			ch <- resp
		}
	}
}

// closeWith marks the client closed with err and abandons all pending
// calls, exactly once.
func (c *Client) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.pending = nil
		c.mu.Unlock()

		c.closeErr = err
		close(c.closeCh)
		c.cancel()
	})
}

// call issues a request and blocks for the matching response, honouring ctx
// cancellation and client Close.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	ch := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.pending == nil {
		c.mu.Unlock()
		return nil, errors.New(errClosedMsg)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return nil, fmt.Errorf("mcp: encode request: %w", err)
	}

	c.sendMu.Lock()
	err = c.transport.Send(ctx, b)
	c.sendMu.Unlock()
	if err != nil {
		c.removePending(id)
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, &RPCError{Code: resp.Error.Code, Message: resp.Error.Message}
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.closeCh:
		return nil, c.closeErr
	}
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	if c.pending != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// serverResponseWire is the wire shape of the client's reply to a
// server-initiated request. ID is the raw id from the server's request,
// echoed back verbatim (int or string, per JSON-RPC 2.0).
type serverResponseWire struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErrorObj    `json:"error,omitempty"`
}

// respondServerResult sends a successful reply to a server-initiated
// request, with the same id, serialized via sendMu like any other write.
func (c *Client) respondServerResult(id json.RawMessage, result any) {
	resp := serverResponseWire{JSONRPC: "2.0", ID: id, Result: result}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.transport.Send(c.ctx, b)
}

// respondServerError sends a JSON-RPC error reply to a server-initiated
// request, with the same id.
func (c *Client) respondServerError(id json.RawMessage, code int, message string) {
	resp := serverResponseWire{JSONRPC: "2.0", ID: id, Error: &rpcErrorObj{Code: code, Message: message}}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.transport.Send(c.ctx, b)
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	n := rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("mcp: encode notification: %w", err)
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.transport.Send(ctx, b)
}

// Close shuts down the receive loop, abandons any pending calls with a
// "mcp: client closed" error, and closes the underlying transport.
func (c *Client) Close() error {
	c.closeWith(errors.New(errClosedMsg))
	err := c.transport.Close()
	<-c.loopDone
	return err
}
