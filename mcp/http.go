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

	mu        sync.Mutex
	sessionID string

	recvCh chan json.RawMessage

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
// Close is a no-op beyond marking the transport closed (there is no
// persistent connection or session-termination handshake to perform); any
// blocked Receive is unblocked and returns an error.
func NewStreamableHTTPTransport(url string, headers map[string]string) Transport {
	return &httpTransport{
		url:     url,
		headers: headers,
		client:  http.DefaultClient,
		recvCh:  make(chan json.RawMessage, recvQueueSize),
		closed:  make(chan struct{}),
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
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.setSessionID(sid)
	}

	if resp.StatusCode == http.StatusAccepted {
		// Notification acknowledged; no body, no message to enqueue.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
		for ev, err := range sse.Scan(resp.Body) {
			if err != nil {
				return fmt.Errorf("mcp: read event stream: %w", err)
			}
			if ev.Data == "" {
				continue
			}
			if enqErr := t.enqueue(ctx, json.RawMessage(ev.Data)); enqErr != nil {
				return enqErr
			}
		}
		return nil
	default:
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

// Close implements Transport. It marks the transport closed, unblocking any
// pending Receive; there is no underlying connection to tear down.
func (t *httpTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
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
