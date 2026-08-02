package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
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
