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
	"time"
)

// errClosedMsg is the error text used when a call is abandoned because the
// client was closed.
const errClosedMsg = "mcp: client closed"

// rpcInternalError is the standard JSON-RPC "Internal error" code. It is
// used both for the "server busy" reply when the server-request dispatch
// semaphore is full and for a handler that returns an error (see
// handleElicitationCreate) — in both cases the failure is on the client
// side, not a well-formed protocol-level rejection like -32601/-32602.
const rpcInternalError = -32603

// maxConcurrentServerDispatch bounds the number of server-initiated
// requests (e.g. "elicitation/create") dispatched concurrently. Without a
// bound, a malicious or buggy server flooding the client with server
// requests would spawn unbounded goroutines. A server request that arrives
// while the bound is already saturated is rejected immediately with a
// JSON-RPC error rather than queued or blocked, so recvLoop is never stalled
// waiting for dispatch capacity.
const maxConcurrentServerDispatch = 8

// busyReplyTimeout bounds how long recvLoop will wait while writing the
// synchronous "server busy" rejection reply (see the default case in
// recvLoop's dispatch select). That reply is written directly on recvLoop,
// not from a spawned goroutine — rejecting a request when the dispatch
// bound is saturated must not itself consume a dispatch slot or spawn an
// unbounded goroutine per rejection. But that also means an unbounded write
// there (e.g. against a stuck stdio child that isn't draining stdin) would
// stall recvLoop indefinitely, defeating the reason bounded dispatch exists
// in the first place. Deriving a short-deadline context for just this write
// keeps it best-effort: if the bounded send fails or times out, the reply
// is simply dropped and the server is left to time out its own request.
const busyReplyTimeout = 200 * time.Millisecond

// sendSem is a 1-slot channel-based binary semaphore backing Client's write
// serialization for transports that need it (see Client.selfSerializes and
// selfSerializingTransport in transport.go). A channel, rather than
// sync.Mutex, is used because it lets every acquisition site select on a
// ctx deadline uniformly via TryLockContext: sync.Mutex.Lock has no way to
// time out or observe ctx cancellation while waiting. That's what lets the
// saturated-dispatch "server busy" reply path (recvLoop, via
// sendServerResponse — see busyReplyTimeout) bound its acquisition to
// ~200ms and drop the reply if it can't get the slot in time, instead of
// letting a stuck stdio write wedge recvLoop indefinitely.
type sendSem chan struct{}

// newSendSem returns a sendSem with its single slot available.
func newSendSem() sendSem {
	s := make(sendSem, 1)
	s <- struct{}{}
	return s
}

// Lock blocks until the slot is acquired. Pair with a deferred Unlock,
// exactly like sync.Mutex.
func (s sendSem) Lock() { <-s }

// Unlock releases the slot. Must be called exactly once per successful Lock
// or successful (true-returning) TryLockContext call.
func (s sendSem) Unlock() { s <- struct{}{} }

// TryLockContext attempts to acquire the slot, blocking until it succeeds
// or ctx is done, whichever comes first. It returns false if ctx finished
// first — in which case the slot was NOT acquired and Unlock must not be
// called.
func (s sendSem) TryLockContext(ctx context.Context) bool {
	select {
	case <-s:
		return true
	case <-ctx.Done():
		return false
	}
}

// closeDrainGrace bounds how long Client.Close waits for in-flight
// server-request dispatch goroutines (dispatchWG) to finish before
// returning anyway. ElicitationHandler implementations are documented to
// respect the ctx they're given (which Close cancels), so in the normal
// case drain finishes almost immediately; this grace is a backstop against
// a handler that ignores ctx and blocks indefinitely (on an unrelated
// channel, a UI prompt, a stuck downstream call, ...), which would
// otherwise make Close hang forever. A dispatch goroutine still running
// after the grace elapses is harmless to leave detached: the transport is
// already closed by the time Close reaches this wait, so any respondServer*
// call it eventually makes fails fast against the closed transport instead
// of corrupting client state.
const closeDrainGrace = 2 * time.Second

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

	nextID atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rpcResponse

	// serverCaps is the raw "capabilities" object from the server's
	// "initialize" response, keyed by top-level capability name (e.g.
	// "resources", "prompts"). Guarded by mu. Nil until Initialize succeeds.
	serverCaps map[string]json.RawMessage

	// negotiatedProtocolVersion is the protocolVersion the server returned
	// in its "initialize" response, once accepted. Guarded by mu. Empty
	// until Initialize succeeds.
	negotiatedProtocolVersion string

	// elicitationHandler, if set, is invoked for server-initiated
	// "elicitation/create" requests. Guarded by mu.
	elicitationHandler ElicitationHandler

	// dispatchSem bounds the number of server-initiated requests dispatched
	// concurrently (see maxConcurrentServerDispatch): recvLoop acquires a
	// slot before spawning a dispatch goroutine and rejects the request with
	// a JSON-RPC error instead of spawning if none is free.
	dispatchSem chan struct{}

	// dispatchWG tracks in-flight dispatch goroutines spawned by recvLoop.
	// Close waits on it (after recvLoop has stopped, so no further Add can
	// race with Wait) so that no dispatch goroutine outlives Close and
	// possibly calls transport.Send after the transport is closed.
	dispatchWG sync.WaitGroup

	// sendSem serializes writes to the transport: Transport only guarantees
	// safety for one concurrent writer, but Client.call may be invoked by
	// multiple goroutines at once (concurrent in-flight RPC calls). It is
	// only actually acquired when !selfSerializes — see that field's doc.
	sendSem sendSem

	// selfSerializes is true when transport implements
	// selfSerializingTransport and reports SelfSerializes() == true (e.g.
	// httpTransport). Determined once at NewClient and never modified after,
	// so every Send site can branch on it directly with no per-call type
	// assertion. When true, sendSem is not acquired around transport.Send:
	// the transport is already safe for concurrent Send, and holding a
	// client-wide lock across it would head-of-line-block every other
	// concurrent call (and server-request reply) behind e.g. one call's HTTP
	// retry backoff for no correctness reason. When false (the stdio
	// framedTransport, and any transport that doesn't opt in), sendSem is
	// held exactly as before: interleaved concurrent writes would corrupt
	// stdio's newline framing.
	selfSerializes bool

	closeOnce sync.Once
	closeCh   chan struct{}
	closeErr  error

	// transportCloseErr is the error, if any, returned by the transport.Close
	// call made from inside closeWith (guarded by closeOnce, so it's safe to
	// read after closeWith has returned — see Client.Close).
	transportCloseErr error

	loopDone chan struct{}
}

// NewClient wraps t. Call Initialize before making any other call.
func NewClient(t Transport) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		transport:   t,
		ctx:         ctx,
		cancel:      cancel,
		pending:     make(map[int64]chan rpcResponse),
		closeCh:     make(chan struct{}),
		loopDone:    make(chan struct{}),
		dispatchSem: make(chan struct{}, maxConcurrentServerDispatch),
		sendSem:     newSendSem(),
	}
	if ss, ok := t.(selfSerializingTransport); ok {
		c.selfSerializes = ss.SelfSerializes()
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
			//
			// Dispatch is bounded by dispatchSem: acquiring it (instead of
			// spawning unconditionally) caps the number of concurrent
			// dispatch goroutines, protecting against a server flooding us
			// with server-initiated requests. If the bound is already
			// saturated, the request is rejected immediately with a
			// JSON-RPC error rather than spawned or queued, so recvLoop
			// itself never blocks waiting for dispatch capacity.
			req := serverRequest{ID: resp.ID, Method: resp.Method, Params: resp.Params}
			select {
			case c.dispatchSem <- struct{}{}:
				c.dispatchWG.Add(1)
				go func() {
					defer c.dispatchWG.Done()
					defer func() { <-c.dispatchSem }()
					c.dispatchServerRequest(req)
				}()
			default:
				// Bounded, best-effort: see busyReplyTimeout for why this
				// write must not be allowed to block recvLoop indefinitely.
				busyCtx, busyCancel := context.WithTimeout(c.ctx, busyReplyTimeout)
				c.sendServerResponse(busyCtx, serverResponseWire{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &rpcErrorObj{Code: rpcInternalError, Message: "server busy"},
				})
				busyCancel()
			}
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

// closeWith marks the client closed with err, abandons all pending calls,
// and closes the underlying transport, exactly once. Closing the transport
// here (not just from Client.Close) matters for the recvLoop error path: a
// transport read error (e.g. the child process died, or the connection
// dropped) reaches here via recvLoop, and without this the transport would
// never be closed on that path — leaking a zombie child process and, for
// the stdio transport, a readLoop goroutine parked forever waiting for a
// Close that never comes. transport.Close is required to be idempotent, so
// the later explicit call from Client.Close (the normal shutdown path) is
// safe even after closeWith already closed it here.
func (c *Client) closeWith(err error) {
	c.closeOnce.Do(func() {
		// closeErr and pending are set together under mu so that any call()
		// which observes pending == nil is guaranteed (by mu's
		// happens-before) to also observe the final closeErr, not a
		// zero-value error from before this closeWith ran.
		c.mu.Lock()
		c.closeErr = err
		c.pending = nil
		c.mu.Unlock()

		close(c.closeCh)
		c.cancel()
		c.transportCloseErr = c.transport.Close()
	})
}

// call issues a request and blocks for the matching response, honouring ctx
// cancellation and client Close.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)

	c.mu.Lock()
	if c.pending == nil {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New(errClosedMsg)
		}
		return nil, err
	}
	c.pending[id] = ch
	c.mu.Unlock()

	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	b, err := json.Marshal(req)
	if err != nil {
		c.removePending(id)
		return nil, fmt.Errorf("mcp: encode request: %w", err)
	}

	if c.selfSerializes {
		err = c.transport.Send(ctx, b)
	} else if c.sendSem.TryLockContext(ctx) {
		err = c.transport.Send(ctx, b)
		c.sendSem.Unlock()
	} else {
		err = ctx.Err()
	}
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

// sendServerResponse marshals and writes resp (a reply to a server-initiated
// request), serialized via sendSem like any other write on a transport that
// doesn't self-serialize. ctx bounds both the sendSem acquisition and the
// write itself; callers that don't need a tighter bound than the client's
// lifetime pass c.ctx (see respondServerResult/respondServerError — this
// behaves like an ordinary blocking acquire, since c.ctx is only cancelled
// by Close), while the saturated-dispatch "server busy" path in recvLoop
// passes a short-deadline ctx instead (see busyReplyTimeout): if sendSem
// can't be acquired within that deadline — e.g. a stuck stdio write is
// holding it — the reply is dropped rather than letting recvLoop (which
// calls this synchronously, not from a spawned goroutine) block
// indefinitely. A dropped busy-reply just means the server times out that
// request on its own.
func (c *Client) sendServerResponse(ctx context.Context, resp serverResponseWire) {
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if c.selfSerializes {
		_ = c.transport.Send(ctx, b)
		return
	}
	if !c.sendSem.TryLockContext(ctx) {
		return
	}
	defer c.sendSem.Unlock()
	_ = c.transport.Send(ctx, b)
}

// respondServerResult sends a successful reply to a server-initiated
// request, with the same id.
func (c *Client) respondServerResult(id json.RawMessage, result any) {
	c.sendServerResponse(c.ctx, serverResponseWire{JSONRPC: "2.0", ID: id, Result: result})
}

// respondServerError sends a JSON-RPC error reply to a server-initiated
// request, with the same id.
func (c *Client) respondServerError(id json.RawMessage, code int, message string) {
	c.sendServerResponse(c.ctx, serverResponseWire{JSONRPC: "2.0", ID: id, Error: &rpcErrorObj{Code: code, Message: message}})
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	n := rpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("mcp: encode notification: %w", err)
	}
	if c.selfSerializes {
		return c.transport.Send(ctx, b)
	}
	if !c.sendSem.TryLockContext(ctx) {
		return ctx.Err()
	}
	defer c.sendSem.Unlock()
	return c.transport.Send(ctx, b)
}

// Close shuts down the receive loop, abandons any pending calls with a
// "mcp: client closed" error, and closes the underlying transport (via
// closeWith, which performs the actual transport.Close call — see its doc).
// It also waits (up to closeDrainGrace) for any in-flight server-request
// dispatch goroutines (dispatchWG) to finish before returning, so that in
// the normal case — a well-behaved ElicitationHandler that respects the ctx
// it's given — no dispatch goroutine outlives Close. A handler that ignores
// ctx and blocks indefinitely cannot make Close hang forever: after
// closeDrainGrace, Close returns anyway (see that constant's doc for why
// leaving such a goroutine running is safe).
//
// Ordering matters: closeWith cancels c.ctx (so a ctx-respecting handler in
// a dispatch goroutine exits promptly) and closes the transport (so
// recvLoop's blocked Receive call returns an error and recvLoop exits,
// closing loopDone). Only after <-c.loopDone — meaning recvLoop has
// definitely stopped and will spawn no further dispatch goroutines — is it
// safe to wait on dispatchWG: sync.WaitGroup forbids a positive-delta Add
// racing with a Wait call that could observe a zero counter, and waiting
// immediately after closeWith (while recvLoop might still be spawning new
// dispatch goroutines) would risk exactly that race.
func (c *Client) Close() error {
	c.closeWith(errors.New(errClosedMsg))
	<-c.loopDone

	drained := make(chan struct{})
	go func() {
		c.dispatchWG.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(closeDrainGrace):
	}

	return c.transportCloseErr
}
