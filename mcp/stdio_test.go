package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// nopWriteCloser adapts a bytes.Buffer (or any io.Writer) to io.WriteCloser
// for tests that don't need real Close semantics on the write side.
type nopWriteCloser struct {
	io.Writer
	closed bool
}

func (w *nopWriteCloser) Close() error {
	w.closed = true
	return nil
}

func TestFramedTransportSend(t *testing.T) {
	var buf bytes.Buffer
	wc := &nopWriteCloser{Writer: &buf}
	tr := newFramedTransport(strings.NewReader(""), wc, nil)
	defer tr.Close()

	ctx := context.Background()
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"pong"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2: %q", len(lines), buf.String())
	}
	if lines[0] != `{"jsonrpc":"2.0","id":1,"method":"ping"}` {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != `{"jsonrpc":"2.0","id":2,"method":"pong"}` {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestFramedTransportSendRejectsEmbeddedNewline(t *testing.T) {
	var buf bytes.Buffer
	wc := &nopWriteCloser{Writer: &buf}
	tr := newFramedTransport(strings.NewReader(""), wc, nil)
	defer tr.Close()

	ctx := context.Background()
	err := tr.Send(ctx, json.RawMessage("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\",\n\"params\":1}"))
	if err == nil {
		t.Fatal("Send: want error for message with an embedded newline, got nil")
	}
	if buf.Len() != 0 {
		t.Fatalf("Send wrote %q despite rejecting the message", buf.String())
	}
}

func TestFramedTransportReceive(t *testing.T) {
	r, w := io.Pipe()
	tr := newFramedTransport(r, &nopWriteCloser{Writer: io.Discard}, nil)
	defer tr.Close()

	go func() {
		_, _ = io.WriteString(w, "{\"a\":1}\n")
		_, _ = io.WriteString(w, "{\"b\":2}\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	msg1, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg1) != `{"a":1}` {
		t.Errorf("msg1 = %q", msg1)
	}

	msg2, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg2) != `{"b":2}` {
		t.Errorf("msg2 = %q", msg2)
	}
}

func TestFramedTransportReceiveEOF(t *testing.T) {
	r, w := io.Pipe()
	tr := newFramedTransport(r, &nopWriteCloser{Writer: io.Discard}, nil)
	defer tr.Close()

	go func() {
		_, _ = io.WriteString(w, "{\"a\":1}\n")
		_ = w.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if _, err := tr.Receive(ctx); err != nil {
		t.Fatalf("first Receive: %v", err)
	}
	if _, err := tr.Receive(ctx); err == nil {
		t.Fatal("second Receive: want error at EOF, got nil")
	}
}

func TestFramedTransportReceiveCtxCancel(t *testing.T) {
	r, _ := io.Pipe() // nothing written, Receive should block until ctx cancel
	tr := newFramedTransport(r, &nopWriteCloser{Writer: io.Discard}, nil)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tr.Receive(ctx)
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want error after ctx cancel, got nil")
		}
	case <-time.After(testTimeout):
		t.Fatal("Receive did not return after ctx cancellation")
	}
}

func TestFramedTransportCloseCallsCloseFn(t *testing.T) {
	r, _ := io.Pipe()
	wc := &nopWriteCloser{Writer: io.Discard}
	called := 0
	tr := newFramedTransport(r, wc, func() error { called++; return nil })

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !wc.closed {
		t.Error("writer was not closed")
	}
	if called != 1 {
		t.Errorf("closeFn called %d times, want 1", called)
	}

	// Second Close must not call closeFn again.
	if err := tr.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if called != 1 {
		t.Errorf("closeFn called %d times after second Close, want 1", called)
	}
}

func TestFramedTransportOverPipeEndToEnd(t *testing.T) {
	// Two framedTransports wired back to back over io.Pipe, simulating the
	// stdio subprocess framing without spawning a real process.
	aR, bW := io.Pipe()
	bR, aW := io.Pipe()

	a := newFramedTransport(aR, &nopWriteCloser{Writer: aW}, nil)
	b := newFramedTransport(bR, &nopWriteCloser{Writer: bW}, nil)
	defer a.Close()
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// io.Pipe is unbuffered: Write blocks until a matching Read happens, so
	// Send and Receive on either side must run concurrently, not
	// sequentially in the same goroutine.
	sendErr := make(chan error, 1)
	go func() {
		sendErr <- a.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	}()
	msg, err := b.Receive(ctx)
	if err != nil {
		t.Fatalf("b.Receive: %v", err)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("a.Send: %v", err)
	}
	if string(msg) != `{"jsonrpc":"2.0","id":1,"method":"initialize"}` {
		t.Errorf("msg = %q", msg)
	}

	go func() {
		sendErr <- b.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}()
	reply, err := a.Receive(ctx)
	if err != nil {
		t.Fatalf("a.Receive: %v", err)
	}
	if err := <-sendErr; err != nil {
		t.Fatalf("b.Send: %v", err)
	}
	if string(reply) != `{"jsonrpc":"2.0","id":1,"result":{}}` {
		t.Errorf("reply = %q", reply)
	}
}

func TestFramedTransportLargeLine(t *testing.T) {
	r, w := io.Pipe()
	tr := newFramedTransport(r, &nopWriteCloser{Writer: io.Discard}, nil)
	defer tr.Close()

	big := strings.Repeat("x", 1024*1024) // 1MB, well under the 10MB cap
	payload := `{"jsonrpc":"2.0","id":1,"result":"` + big + `"}`
	go func() {
		_, _ = io.WriteString(w, payload+"\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	msg, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(msg) != len(payload) {
		t.Fatalf("got %d bytes, want %d", len(msg), len(payload))
	}
}

// TestFramedTransportRepeatedTimeoutThenMessage reproduces the scenario a
// reviewer found data-racing an earlier implementation that spawned a new
// scanning goroutine per Receive call: several short-timeout Receives
// against a quiet pipe (no data yet), each abandoning its goroutine mid-scan,
// followed by a write and a Receive that must actually get the message —
// not lose it to one of the abandoned goroutines, and not race the
// underlying scanner. Must be run with -race.
func TestFramedTransportRepeatedTimeoutThenMessage(t *testing.T) {
	r, w := io.Pipe()
	tr := newFramedTransport(r, &nopWriteCloser{Writer: io.Discard}, nil)
	defer tr.Close()

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		_, err := tr.Receive(ctx)
		cancel()
		if err == nil {
			t.Fatalf("Receive %d: want timeout error, got nil (pipe should still be quiet)", i)
		}
	}

	go func() {
		_, _ = io.WriteString(w, "{\"hello\":\"world\"}\n")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	msg, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("final Receive: %v", err)
	}
	if string(msg) != `{"hello":"world"}` {
		t.Fatalf("msg = %q, want %q", msg, `{"hello":"world"}`)
	}
}

// TestFramedTransportSendCtxCancelOnBlockedWrite pins fix #1: Send over a
// real *os.File writer must be interrupted by ctx cancellation even while
// blocked inside the underlying Write syscall (a full pipe buffer, nobody
// reading the other end — the same situation as a hung child process that
// stopped reading its stdin). Before the fix, Send only checked ctx.Err()
// once up front and then called a bare blocking Write, so this would hang
// forever; since Client.call holds sendMu for the whole Send, that would
// also wedge every other concurrent RPC on the client, not just this one.
func TestFramedTransportSendCtxCancelOnBlockedWrite(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	tr := newFramedTransport(strings.NewReader(""), pw, nil)
	defer tr.Close()

	// A message large enough to exceed any realistic OS pipe buffer size
	// (typically 16-64KB), so the underlying Write blocks partway through
	// since nothing ever reads from pr in this test.
	big := bytes.Repeat([]byte("x"), 32<<20) // 32MB
	msg := json.RawMessage(append(append([]byte{'"'}, big...), '"'))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- tr.Send(ctx, msg) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send: want error from ctx cancellation on a blocked write, got nil")
		}
		if elapsed := time.Since(start); elapsed > testTimeout {
			t.Fatalf("Send took %v to return after ctx cancellation, want well under %v", elapsed, testTimeout)
		}
	case <-time.After(testTimeout):
		t.Fatal("Send did not return after ctx cancellation on a blocked write — the wedge fix #1 targets is still present")
	}

	// The interrupted write may have left a partial line on the wire, so
	// the transport must now refuse further Sends (abandoned) rather than
	// risk corrupting the framing further.
	if err := tr.Send(context.Background(), json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("Send after an interrupted write: want error (transport abandoned), got nil")
	}
}

// TestFramedTransportSendCtxCancelZeroBytesDoesNotAbandon covers the n==0
// refinement to fix #1: if a write is interrupted by ctx cancellation
// before any bytes reached the wire, framing is provably intact (this Send
// contributed nothing to the stream), so the transport must NOT be
// abandoned — unlike the partial-write (n>0) case tested by
// TestFramedTransportSendCtxCancelOnBlockedWrite.
//
// It saturates the pipe's kernel buffer completely first (via short
// per-attempt write deadlines, stopping once two consecutive attempts make
// zero progress — i.e. there is no free space left at all), so the
// subsequent Send's very first internal write attempt blocks with zero
// bytes accepted before the ctx deadline fires.
func TestFramedTransportSendCtxCancelZeroBytesDoesNotAbandon(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	// Saturate the pipe's kernel buffer: keep attempting bounded writes
	// until two in a row make no progress at all, meaning free space is
	// exhausted.
	zeroStreak := 0
	for zeroStreak < 2 {
		if err := pw.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
			t.Fatalf("SetWriteDeadline: %v", err)
		}
		n, werr := pw.Write(bytes.Repeat([]byte("y"), 4096))
		if n == 0 && werr != nil {
			zeroStreak++
		} else {
			zeroStreak = 0
		}
	}
	if err := pw.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatalf("clear SetWriteDeadline: %v", err)
	}

	tr := newFramedTransport(strings.NewReader(""), pw, nil)
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- tr.Send(ctx, json.RawMessage(`{"a":1}`)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send: want error from ctx cancellation on a fully-saturated pipe, got nil")
		}
	case <-time.After(testTimeout):
		t.Fatal("Send did not return after ctx cancellation")
	}

	tr.writeMu.Lock()
	abandoned := tr.abandoned
	tr.writeMu.Unlock()
	if abandoned {
		t.Fatal("transport marked abandoned after a write that contributed zero bytes to the wire, want not abandoned")
	}
}

// TestFramedTransportSendSubprocessRoundTripStillWorks is a companion to
// the ctx-cancel-on-blocked-write test above: it asserts the ctx-aware
// write path introduced by fix #1 doesn't break the common case of a
// well-behaved peer that reads promptly, over a real subprocess (not just
// the in-memory pipe transports used elsewhere in this file).
func TestFramedTransportSendSubprocessRoundTripStillWorks(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}
	tr, err := NewStdioTransport([]string{"cat"}, nil)
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	for i := 0; i < 5; i++ {
		msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if err := tr.Send(ctx, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		echoed, err := tr.Receive(ctx)
		if err != nil {
			t.Fatalf("Receive %d: %v", i, err)
		}
		if string(echoed) != string(msg) {
			t.Fatalf("echoed %d = %q, want %q", i, echoed, msg)
		}
	}
}

// TestNewStdioTransportSubprocess sanity-checks the real exec.Cmd plumbing
// (env, stdin/stdout wiring, Close) against `cat`, which just echoes stdin
// to stdout — a stand-in for a well-behaved MCP server for framing purposes.
func TestNewStdioTransportSubprocess(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not available")
	}

	tr, err := NewStdioTransport([]string{"cat"}, []string{"FOO=bar"})
	if err != nil {
		t.Fatalf("NewStdioTransport: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	echoed, err := tr.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(echoed) != string(msg) {
		t.Fatalf("echoed = %q, want %q", echoed, msg)
	}

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// chunkedWriteCloser wraps an io.WriteCloser and deliberately splits every
// Write into two separate underlying Write calls with a short sleep between
// them. It exists to make a framing race easy to observe: if two goroutines
// ever call Write concurrently without external serialization, the sleep
// between the two halves of one message maximizes the odds that another
// goroutine's write lands in between them, corrupting the newline framing.
// It is intentionally not internally synchronized (nothing here should ever
// be called concurrently if the layers above it — framedTransport's
// writeMu, and Client's sendSem for non-self-serializing transports — are
// doing their job).
type chunkedWriteCloser struct {
	w io.WriteCloser
}

func (c *chunkedWriteCloser) Write(p []byte) (int, error) {
	if len(p) <= 4 {
		return c.w.Write(p)
	}
	mid := len(p) / 2
	n1, err := c.w.Write(p[:mid])
	if err != nil {
		return n1, err
	}
	time.Sleep(3 * time.Millisecond)
	n2, err := c.w.Write(p[mid:])
	return n1 + n2, err
}

func (c *chunkedWriteCloser) Close() error { return c.w.Close() }

// TestStdioClientConcurrentCallsStaySerialized is the explicit
// concurrency-framing regression test: the stdio framedTransport does not
// implement selfSerializingTransport (see transport.go), so a Client
// wrapping it must still serialize writes (via sendSem) even though the
// self-serializing HTTP path no longer holds that same lock across Send.
// Many goroutines calling Client.call concurrently over stdio must never
// interleave their writes and corrupt the newline framing — every line the
// fake "server" side observes must be exactly one complete, valid JSON-RPC
// request.
func TestStdioClientConcurrentCallsStaySerialized(t *testing.T) {
	aR, bW := io.Pipe()
	bR, aW := io.Pipe()

	clientTr := newFramedTransport(aR, &chunkedWriteCloser{w: &nopWriteCloser{Writer: aW}}, nil)
	serverTr := newFramedTransport(bR, &nopWriteCloser{Writer: bW}, nil)
	defer clientTr.Close()
	defer serverTr.Close()

	if _, ok := any(clientTr).(selfSerializingTransport); ok {
		t.Fatal("framedTransport must not implement selfSerializingTransport")
	}

	c := NewClient(clientTr)
	defer c.Close()
	if c.selfSerializes {
		t.Fatal("Client.selfSerializes must be false for the stdio transport")
	}

	const n = 20
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for i := 0; i < n; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			raw, err := serverTr.Receive(ctx)
			cancel()
			if err != nil {
				t.Errorf("server receive %d: %v", i, err)
				return
			}
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				t.Errorf("server received a corrupted/unparseable line: %v (raw=%q)", err, raw)
				continue
			}
			resp := json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID))
			sendCtx, sendCancel := context.WithTimeout(context.Background(), testTimeout)
			if err := serverTr.Send(sendCtx, resp); err != nil {
				t.Errorf("server send %d: %v", i, err)
			}
			sendCancel()
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			if _, err := c.call(ctx, fmt.Sprintf("method-%d", i), nil); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	select {
	case <-serverDone:
	case <-time.After(testTimeout):
		t.Fatal("server loop did not finish processing all requests")
	}
}
