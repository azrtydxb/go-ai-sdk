package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPTransportDirectJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Accept") != "application/json, text/event-stream" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	var got struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != 1 {
		t.Fatalf("id = %d, want 1", got.ID)
	}
}

// TestHTTPTransportCloseInterruptsStalledJSONBody pins fix #3: Close must
// interrupt a hung application/json response body read, not just SSE
// (text/event-stream) bodies. Before the fix, only SSE bodies were
// registered in openBodies, so a server that sent headers (declaring
// Content-Type: application/json) and then never finished the body would
// leave sendOnce's io.ReadAll blocked forever, surviving Close.
func TestHTTPTransportCloseInterruptsStalledJSONBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		// Deliberately incomplete body with no Content-Length: the client
		// has no way to know it's "done" and must keep reading.
		fmt.Fprint(w, `{"jsonrpc":"2.0","id`)
		fl.Flush()
		<-release // hang until the test lets the handler return
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	tr := NewStreamableHTTPTransport(srv.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sendDone := make(chan error, 1)
	go func() { sendDone <- tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)) }()

	// Give sendOnce a moment to reach the (now stalled) io.ReadAll so the
	// body is actually tracked before Close runs, exercising the real
	// interrupt path rather than a lucky race where Close beats Send there.
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Close did not return promptly — a stalled JSON body was not interrupted (fix #3)")
	}

	select {
	case err := <-sendDone:
		if err == nil {
			t.Fatal("Send: want an error once Close interrupted the stalled JSON body read, got nil")
		}
	case <-time.After(testTimeout):
		t.Fatal("Send (blocked reading a stalled JSON body) did not return after Close")
	}
}

func TestHTTPTransportSSEMultipleMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support Flush")
		}
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"n\":1}}\n\n")
		fl.Flush()
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":2,\"result\":{\"n\":2}}\n\n")
		fl.Flush()
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","method":"batch"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var ids []int
	for i := 0; i < 2; i++ {
		msg, err := tr.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive[%d]: %v", i, err)
		}
		var got struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal(msg, &got); err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		ids = append(ids, got.ID)
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v, want [1 2] (order preserved)", ids)
	}
}

func TestHTTPTransportSessionIDEcho(t *testing.T) {
	const sessionID = "sess-abc-123"
	var gotSessionOnSecond string
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		gotSessionOnSecond = r.Header.Get("Mcp-Session-Id")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive 1: %v", err)
	}
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"b"}`)); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive 2: %v", err)
	}
	if gotSessionOnSecond != sessionID {
		t.Fatalf("second request Mcp-Session-Id = %q, want %q", gotSessionOnSecond, sessionID)
	}
}

func TestHTTPTransportExtraHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, map[string]string{"Authorization": "Bearer tok123"})
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"a"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer tok123")
	}
}

func TestHTTPTransportNotificationNoMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// No message should have been queued; Receive should time out.
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer shortCancel()
	if _, err := tr.Receive(shortCtx); err == nil {
		t.Fatal("Receive returned a message after a notification response, want timeout error")
	}
}

// TestHTTPTransportCloseInterruptsStalled202Body pins the fix #3 residual:
// the 202 Accepted (notification-ack) body read must be tracked and
// interruptible by Close too, not just the SSE and JSON success paths. This
// path runs on every session's "notifications/initialized" handshake step,
// so a server that stalls it isn't a rare edge case.
func TestHTTPTransportCloseInterruptsStalled202Body(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		fl := w.(http.Flusher)
		fmt.Fprint(w, "not valid json, and deliberately never finished")
		fl.Flush()
		<-release // hang until the test lets the handler return
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	tr := NewStreamableHTTPTransport(srv.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	}()

	// Give sendOnce a moment to reach the (now stalled) 202 body read so it's
	// actually tracked before Close runs.
	time.Sleep(50 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Close did not return promptly — a stalled 202 body was not interrupted (fix #3 residual)")
	}

	select {
	case <-sendDone:
		// Either a nil (the read simply got cut off cleanly, which is fine —
		// Send never enqueues a message on a 202 either way) or a non-nil
		// error is acceptable; what matters is that it returned at all.
	case <-time.After(testTimeout):
		t.Fatal("Send (blocked reading a stalled 202 body) did not return after Close")
	}
}

// TestHTTPTransportSSEBodyStaysOpen exercises the deviation-from-SHOULD case
// the doc comment on NewStreamableHTTPTransport calls out: a server that
// sends its JSON-RPC response over SSE but keeps the response body open
// afterward (permitted, since the spec only says the server SHOULD close
// it). Send must not wedge behind that open body — CallTool must complete
// promptly — and Close must release the drain goroutine blocked reading it.
func TestHTTPTransportSSEBodyStaysOpen(t *testing.T) {
	bodyReleased := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl := w.(http.Flusher)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}],\"isError\":false}}\n\n", *req.ID)
			fl.Flush()
			// Deliberately keep the body open (no return) until the test
			// tells the client to Close, to simulate a server that doesn't
			// close the stream after responding.
			<-bodyReleased
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	c := NewClient(tr)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// CallTool must complete even though the server keeps the SSE body
	// open; Send returning promptly (not blocking on the drain) is what
	// makes this possible.
	callDone := make(chan error, 1)
	go func() {
		res, err := c.CallTool(ctx, "echo", json.RawMessage(`{}`))
		if err == nil && res.Text != "hi" {
			err = fmt.Errorf("Text = %q, want hi", res.Text)
		}
		callDone <- err
	}()
	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("CallTool did not complete while the SSE body stayed open")
	}

	// Close must release the drain goroutine blocked reading the still-open
	// body, and return promptly rather than hanging.
	closeDone := make(chan error, 1)
	go func() { closeDone <- c.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Close did not return while a drain goroutine was blocked")
	}
	close(bodyReleased)
}

// recordingBody wraps an io.ReadCloser, counting Close calls in *closed —
// used to detect whether a response body was actually released or leaked.
type recordingBody struct {
	io.ReadCloser
	closed *int32
}

func (b *recordingBody) Close() error {
	atomic.AddInt32(b.closed, 1)
	return b.ReadCloser.Close()
}

// recordingRoundTripper wraps every response body it sees in a
// recordingBody, so a test can observe whether Send (or anything else)
// ever closed it.
type recordingRoundTripper struct {
	rt     http.RoundTripper
	closed *int32
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.rt.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	resp.Body = &recordingBody{ReadCloser: resp.Body, closed: rt.closed}
	return resp, nil
}

// TestHTTPTransportSendClosedRaceDoesNotLeakBody exercises the race the
// reviewer flagged: Close() runs while a Send's request is still in
// flight, i.e. before that Send has had a chance to register its response
// body with trackBody. The fixture delays the response until after Close()
// has completed its sweep, so if trackBody/Close weren't synchronized
// under one mutex, Send would go on to track and drain a body that Close's
// sweep already missed — and since the server never closes the stream
// (kept open on a channel the test never releases within the test), that
// drain goroutine would block forever reading it: a leaked goroutine and a
// leaked connection that's also never closed.
//
// With the fix, trackBody sees the transport is already closed (the same
// mutex Close used to flip closedFlag and snapshot openBodies) and Send
// closes the body itself instead of hand it to a goroutine.
func TestHTTPTransportSendClosedRaceDoesNotLeakBody(t *testing.T) {
	proceed := make(chan struct{})
	hang := make(chan struct{})
	var hangOnce sync.Once
	releaseHang := func() { hangOnce.Do(func() { close(hang) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-proceed // block until the test has closed the transport
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		fl.Flush()
		<-hang // never close the stream on the server side either
	}))
	// t.Cleanup runs LIFO, like defer: registering releaseHang after
	// srv.Close means releaseHang runs first, unblocking the handler so
	// srv.Close (which waits for outstanding handlers) doesn't hang —
	// including on an early t.Fatal, where releaseHang is never reached
	// as ordinary statement.
	t.Cleanup(srv.Close)
	t.Cleanup(releaseHang)

	var closedBodies int32
	tr := &httpTransport{
		url:        srv.URL,
		client:     &http.Client{Transport: &recordingRoundTripper{rt: http.DefaultTransport, closed: &closedBodies}},
		recvCh:     make(chan json.RawMessage, recvQueueSize),
		openBodies: make(map[io.Closer]struct{}),
		closed:     make(chan struct{}),
	}

	sendErrCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		sendErrCh <- tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`))
	}()

	// Give Send time to get past its initial (non-authoritative) closed
	// check and into the in-flight HTTP request, so Close genuinely races
	// it rather than being observed by that fast path.
	time.Sleep(20 * time.Millisecond)

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	// Let Close's sweep run (it will find zero tracked bodies — the
	// response hasn't arrived yet) before releasing the handler's
	// response, so the race window the fix must close is actually hit.
	time.Sleep(20 * time.Millisecond)
	close(proceed)

	select {
	case err := <-sendErrCh:
		if err == nil {
			t.Fatal("Send: want an error for a request that lost the race with Close, got nil")
		}
	case <-time.After(testTimeout):
		t.Fatal("Send did not return — want it to observe the transport closed and return promptly")
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Close did not return")
	}

	// The body must have been closed by Send itself (since the server
	// never closes its side): a leaked, never-closed body is exactly the
	// bug this test guards against.
	deadline := time.After(testTimeout)
	for {
		if atomic.LoadInt32(&closedBodies) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("closed bodies = %d, want exactly 1 (body leaked)", atomic.LoadInt32(&closedBodies))
		case <-time.After(5 * time.Millisecond):
		}
	}

	tr.mu.Lock()
	openCount := len(tr.openBodies)
	tr.mu.Unlock()
	if openCount != 0 {
		t.Errorf("openBodies = %d, want 0 (no body should remain tracked)", openCount)
	}

	releaseHang() // done: let the handler return so cleanup is quick
}

// TestHTTPTransportIntegrationWithClient exercises the transport through a
// full mcp.Client Initialize + CallTool round trip against an httptest
// server that replies with SSE.
func TestHTTPTransportIntegrationWithClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)

		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-03-26","capabilities":{"tools":{}}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"hi\"}],\"isError\":false}}\n\n", *req.ID)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	c := NewClient(tr)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "hi" {
		t.Fatalf("Text = %q, want hi", res.Text)
	}
}

func TestHTTPTransportSuccessBodyOverCapErrors(t *testing.T) {
	orig := maxSuccessBodyBytes
	maxSuccessBodyBytes = 1024
	defer func() { maxSuccessBodyBytes = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// A direct-JSON response body larger than the cap.
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"`))
		w.Write([]byte(strings.Repeat("x", 4096)))
		w.Write([]byte(`"}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx := context.Background()
	err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err == nil {
		t.Fatal("expected an error when the success body exceeds maxSuccessBodyBytes, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want it to mention the size limit", err)
	}
}

func TestHTTPTransportSuccessBodyUnderCapWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"ok"}`))
	}))
	defer srv.Close()

	tr := NewStreamableHTTPTransport(srv.URL, nil)
	defer tr.Close()
	ctx := context.Background()
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("send: %v", err)
	}
	msg, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !strings.Contains(string(msg), `"ok"`) {
		t.Fatalf("msg = %s", msg)
	}
}
