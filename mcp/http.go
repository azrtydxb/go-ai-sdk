package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/internal/sse"
)

// recvQueueSize bounds the number of received-but-not-yet-drained messages
// an httpTransport will buffer. It's generous relative to how many messages
// a single Send's response can realistically carry (an SSE response with a
// handful of JSON-RPC messages), so Send never blocks waiting for a
// concurrent Receive under normal use.
const recvQueueSize = 64

// httpTransport implements Transport over the MCP Streamable HTTP transport:
// each Send is a POST of one JSON-RPC message; the response is either a
// single application/json body (one received message) or a text/event-stream
// body whose SSE data events each carry one received message. Received
// messages are queued and handed out, in order, by Receive.
//
// There is no persistent connection here (unlike the stdio transport's
// framedTransport): Receive only ever yields data that a prior Send's
// response produced. This matches JSON-RPC request/response flow — a
// Client's recvLoop calls Receive to pick up the response to whatever call
// is currently in flight — but means Receive will block forever if called
// without a corresponding Send having happened (or having one in flight).
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	// mu guards sessionID, openBodies, and closedFlag together: trackBody
	// must check closedFlag and register the body (plus drainWG.Add) as one
	// atomic step, so Close's body sweep can never miss a body whose Send
	// raced it (see trackBody's doc for why).
	mu         sync.Mutex
	sessionID  string
	openBodies map[io.Closer]struct{}
	closedFlag bool

	recvCh chan json.RawMessage

	drainWG sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// NewStreamableHTTPTransport speaks the MCP Streamable HTTP transport: each
// Send POSTs the JSON-RPC message to url (Content-Type application/json,
// Accept "application/json, text/event-stream"); responses arrive either as
// a direct application/json body or as an SSE stream (text/event-stream)
// whose events carry JSON-RPC messages — both are fed to Receive in order.
// The Mcp-Session-Id response header, when present, is captured and echoed
// on subsequent requests. headers are added to every request (e.g.
// Authorization).
//
// For an SSE response, Send hands the body off to a per-response drain
// goroutine that enqueues messages as they arrive (in arrival order) and
// returns as soon as the response headers are read — it does not block
// until the stream ends. This matters because Client serializes all Sends
// behind one mutex: draining synchronously would hold that mutex, and the
// MCP spec only says a server SHOULD close the SSE stream after sending its
// response (2025-03-26), not that it MUST, so a server that keeps the
// stream open would otherwise wedge every subsequent call. Per response,
// only one goroutine drains, so ordering within that response's messages is
// preserved; Close closes any still-open response bodies, which unblocks
// their drain goroutines' reads and lets them exit.
//
// Close marks the transport closed, closes any response bodies still being
// drained, and unblocks any blocked Receive; there is no persistent
// connection or session-termination handshake to perform beyond that.
//
// Known deviations from the full 2025-03-26 Streamable HTTP transport spec
// (v1 is scoped to tools-only MCP clients, which don't need the rest):
//   - Close does not send a DELETE to terminate the session on the server;
//     the session, if any, is simply abandoned.
//   - There is no standalone GET request opening a server-initiated SSE
//     channel, so server-initiated requests/notifications outside of a
//     POST response are not supported.
func NewStreamableHTTPTransport(url string, headers map[string]string) Transport {
	return &httpTransport{
		url:        url,
		headers:    headers,
		client:     http.DefaultClient,
		recvCh:     make(chan json.RawMessage, recvQueueSize),
		openBodies: make(map[io.Closer]struct{}),
		closed:     make(chan struct{}),
	}
}

// Send implements Transport.
func (t *httpTransport) Send(ctx context.Context, msg json.RawMessage) error {
	select {
	case <-t.closed:
		return errors.New("mcp: transport closed")
	default:
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if sid := t.getSessionID(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: http request: %w", err)
	}

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.setSessionID(sid)
	}

	if resp.StatusCode == http.StatusAccepted {
		// Notification acknowledged; no body, no message to enqueue.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return fmt.Errorf("mcp: http status %d: %s", resp.StatusCode, string(body))
	}

	ctype := resp.Header.Get("Content-Type")
	mediaType := ctype
	if ctype != "" {
		if mt, _, err := mime.ParseMediaType(ctype); err == nil {
			mediaType = mt
		}
	}

	switch mediaType {
	case "text/event-stream":
		// Drain asynchronously: see the doc comment on
		// NewStreamableHTTPTransport for why Send must not block here.
		// trackBody is the authoritative closed-check (the select at the
		// top of Send is only a fast path): if Close raced this request
		// and already ran, trackBody reports that atomically and the body
		// is closed here instead of being handed to a goroutine that would
		// never see it swept.
		if !t.trackBody(resp.Body) {
			resp.Body.Close()
			return errors.New("mcp: transport closed")
		}
		go t.drainSSE(resp.Body)
		return nil
	default:
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("mcp: read response body: %w", err)
		}
		if len(body) == 0 {
			return nil
		}
		return t.enqueue(ctx, json.RawMessage(body))
	}
}

// drainSSE reads SSE events from body, enqueuing each one's data in arrival
// order, until the stream ends, the body is closed (by Close, unblocking
// the read), or enqueue fails because the transport closed. It always
// closes body and untracks it before returning.
func (t *httpTransport) drainSSE(body io.ReadCloser) {
	defer t.drainWG.Done()
	defer t.untrackBody(body)
	defer body.Close()

	for ev, err := range sse.Scan(body) {
		if err != nil {
			return
		}
		if ev.Data == "" {
			continue
		}
		if t.enqueue(t.drainCtx(), json.RawMessage(ev.Data)) != nil {
			return
		}
	}
}

// drainCtx is the context used to enqueue messages from a background drain
// goroutine, which has no caller-supplied ctx of its own. It's cancelled
// only via t.closed (handled inside enqueue), so drains never block
// Receive/Close forever.
func (t *httpTransport) drainCtx() context.Context { return context.Background() }

// trackBody registers b as an open SSE response body and reserves a
// drainWG slot for the goroutine about to drain it, unless the transport is
// already closed, in which case it does neither and returns false.
//
// This must be one atomic step under mu: Close also takes mu to flip
// closedFlag and snapshot openBodies for its sweep, so whichever of
// trackBody/Close acquires mu first determines the outcome consistently —
// either trackBody sees closedFlag and backs off (the caller closes b
// itself), or it registers b (and calls drainWG.Add) before Close can
// possibly snapshot openBodies, so Close's sweep is guaranteed to include
// b. Without this, a body whose Send raced a concurrent Close — Send read
// closedFlag as false and proceeded, but Close's sweep already ran before
// Send got here — would be tracked by nobody: it would never be closed
// (leaking the connection) and drainWG.Wait would return before the
// eventual drain goroutine even started (since Add would race Wait too),
// so Close could return while a drain goroutine was about to start and
// block forever reading an unclosed body.
func (t *httpTransport) trackBody(b io.Closer) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closedFlag {
		return false
	}
	t.openBodies[b] = struct{}{}
	t.drainWG.Add(1)
	return true
}

func (t *httpTransport) untrackBody(b io.Closer) {
	t.mu.Lock()
	delete(t.openBodies, b)
	t.mu.Unlock()
}

// enqueue hands msg to Receive, honouring ctx cancellation and Close.
func (t *httpTransport) enqueue(ctx context.Context, msg json.RawMessage) error {
	select {
	case t.recvCh <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.closed:
		return errors.New("mcp: transport closed")
	}
}

// Receive implements Transport.
func (t *httpTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case msg := <-t.recvCh:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, errors.New("mcp: transport closed")
	}
}

// Close implements Transport. It marks the transport closed (unblocking any
// pending Receive), closes any SSE response bodies still being drained
// (unblocking their drain goroutines' reads), and waits for those
// goroutines to finish before returning.
//
// closedFlag is flipped and openBodies snapshotted under mu, the same lock
// trackBody uses to check closedFlag and register a body — see trackBody's
// doc for why that atomicity is what makes this sweep exhaustive even
// against a Send racing this call.
func (t *httpTransport) Close() error {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.closedFlag = true
		bodies := make([]io.Closer, 0, len(t.openBodies))
		for b := range t.openBodies {
			bodies = append(bodies, b)
		}
		t.mu.Unlock()
		close(t.closed)
		for _, b := range bodies {
			_ = b.Close()
		}
	})
	t.drainWG.Wait()
	return nil
}

func (t *httpTransport) getSessionID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

func (t *httpTransport) setSessionID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sessionID = id
}
