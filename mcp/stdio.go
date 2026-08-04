package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// maxLineSize bounds a single newline-delimited JSON-RPC message (mirrors
// internal/sse's line limit).
const maxLineSize = 10 * 1024 * 1024

// framedResult is one line read off the wire, or a terminal error.
type framedResult struct {
	line []byte
	err  error
}

// framedTransport speaks newline-delimited JSON-RPC over an io.Reader /
// io.WriteCloser pair: one JSON object per line, in each direction. It is
// the framing logic behind NewStdioTransport, kept independent of exec.Cmd
// so it can be tested over plain pipes.
//
// A single goroutine, started at construction and living for the
// transport's lifetime, owns the bufio.Scanner exclusively and feeds
// framedResults to msgCh. Receive never spawns its own reader: it only
// selects on msgCh / ctx.Done() / the transport's closed signal. This
// matters because bufio.Scanner is not safe for concurrent use, and a
// design where each Receive call spawned its own scanning goroutine (one
// that outlives a cancelled call) would race two such goroutines against
// the scanner under repeated cancel-and-retry Receive calls — a real risk
// since Transport is a public interface and "call Receive with a short
// timeout, retry" is a legitimate caller pattern.
type framedTransport struct {
	scanner *bufio.Scanner
	msgCh   chan framedResult

	writeMu   sync.Mutex
	w         io.WriteCloser
	abandoned bool // set once a write is interrupted mid-flight by ctx cancellation

	closeOnce sync.Once
	closed    chan struct{}
	closeFn   func() error
}

// newFramedTransport builds a Transport that reads newline-delimited JSON
// from r and writes newline-delimited JSON to w. closeFn is invoked exactly
// once by Close, after w has been closed, to release any additional
// resources (e.g. terminate a child process). closeFn must, directly or
// indirectly, cause r to unblock with EOF or an error (e.g. by killing the
// process that owns the other end of the pipe) — otherwise readLoop's
// goroutine has nothing to make it return and leaks for the life of the
// program.

func newFramedTransport(r io.Reader, w io.WriteCloser, closeFn func() error) *framedTransport {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	t := &framedTransport{
		scanner: sc,
		msgCh:   make(chan framedResult),
		w:       w,
		closed:  make(chan struct{}),
		closeFn: closeFn,
	}
	go t.readLoop()
	return t
}

// readLoop is the sole owner of t.scanner for the transport's lifetime.
func (t *framedTransport) readLoop() {
	for {
		if t.scanner.Scan() {
			b := t.scanner.Bytes()
			cp := make([]byte, len(b))
			copy(cp, b)
			select {
			case t.msgCh <- framedResult{line: cp}:
			case <-t.closed:
				return
			}
			continue
		}

		err := t.scanner.Err()
		if err == nil {
			err = io.EOF
		}
		// Once the underlying reader is exhausted or broken, every future
		// Receive should observe that terminal error rather than block
		// forever, so keep offering it until Close.
		for {
			select {
			case t.msgCh <- framedResult{err: err}:
			case <-t.closed:
				return
			}
		}
	}
}

// Send writes msg as one line. Peers are expected to enforce the same
// (or a more generous) per-line size limit as Receive's maxLineSize; a
// peer with a tighter line buffer may reject or truncate very large
// messages. Per the MCP stdio transport spec, messages must not contain
// embedded newlines; Send rejects any msg that does rather than silently
// corrupting the framing.
//
// Send is ctx-aware even while blocked inside the underlying write: a
// child process that stops reading its stdin (a full pipe buffer) would
// otherwise block Write forever regardless of ctx, and because Client.call
// holds sendSem for the duration of Send (the stdio framedTransport does
// not implement selfSerializingTransport — see transport.go — so its writes
// are still serialized), that would wedge every other
// concurrent RPC on this client too, not just the caller who chose to time
// out. See writeCtx for how the interruption is implemented and why an
// interrupted write abandons the transport.
func (t *framedTransport) Send(ctx context.Context, msg json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if bytes.ContainsRune(msg, '\n') {
		return fmt.Errorf("mcp: message contains an embedded newline, which the stdio transport's line framing forbids")
	}
	line := make([]byte, 0, len(msg)+1)
	line = append(line, msg...)
	line = append(line, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.abandoned {
		return errors.New("mcp: transport abandoned: a previous write was interrupted by context cancellation, which may have left a partial line on the wire")
	}

	_, err := t.writeCtx(ctx, line)
	return err
}

// writeCtx writes line to t.w, arranging for a blocked write to be
// interrupted if ctx is done before it completes — but only when t.w is an
// *os.File: only os.File exposes SetWriteDeadline, and the real stdio
// transport's stdin pipe (exec.Cmd.StdinPipe) is backed by one. For any
// other writer (e.g. the in-memory bytes.Buffer-backed transport used in
// tests, which never blocks) this falls back to a plain blocking Write;
// Send has already checked ctx.Err() once before calling in, so callers
// still get a best-effort ctx check.
//
// Mirrors internal/websocket's runWithContext: a watcher goroutine sets a
// past write deadline on ctx.Done(), and this function always waits for
// that watcher to finish touching the deadline before returning, so a race
// between the watcher firing and Write finishing can never leave a stray
// deadline poisoning a healthy file after control has returned to the
// caller.
//
// A write interrupted by the deadline may have written a partial line
// (io.Writer makes no atomicity guarantee) — that corrupts this stream's
// framing for any peer still reading it. writeCtx only sets t.abandoned
// (writeMu is already held by the caller) when n > 0 proves some bytes
// actually reached the wire; if n == 0, framing is provably intact (this
// Send contributed nothing to the stream) and the transport is left usable
// for the next Send.
func (t *framedTransport) writeCtx(ctx context.Context, line []byte) (int, error) {
	f, ok := t.w.(*os.File)
	if !ok || ctx.Done() == nil {
		return t.w.Write(line)
	}

	stop := make(chan struct{})
	watcherDone := make(chan struct{})
	var interrupted atomic.Bool
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			interrupted.Store(true)
			// SetWriteDeadline's error is intentionally ignored: on a
			// non-pollable file it's a documented no-op error, and for the
			// real stdio transport's pipe-backed *os.File it always
			// succeeds — there's nothing actionable to do with a failure
			// here either way.
			_ = f.SetWriteDeadline(time.Unix(0, 0))
		case <-stop:
		}
	}()

	n, err := f.Write(line)
	close(stop)
	<-watcherDone // don't return until the watcher can no longer touch the deadline unexpectedly

	if !interrupted.Load() {
		return n, err
	}
	if err == nil {
		// The write completed despite ctx firing (a benign race); clear the
		// deadline so the file remains usable and don't report an error for
		// a write that actually succeeded in full. Error intentionally
		// ignored — same rationale as the SetWriteDeadline call above.
		_ = f.SetWriteDeadline(time.Time{})
		return n, nil
	}
	if n > 0 {
		// Some bytes of this line reached the wire before the write was
		// interrupted: the peer may now be looking at a partial JSON-RPC
		// line. Refuse every future Send rather than risk writing after it.
		t.abandoned = true
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		// A genuine write error (e.g. EPIPE from a dead child), not our own
		// deadline firing — surface it directly rather than masking it
		// behind ctx.Err(), so callers can tell "the peer is gone" from
		// "we merely gave up waiting".
		return n, err
	}
	return n, ctx.Err()
}

// Receive returns the next line as a JSON-RPC message. It never loses a
// message that the reader goroutine already read: if a Receive call is
// abandoned via ctx cancellation while the reader goroutine is blocked
// handing off a line, that line is delivered to whichever Receive call
// picks it up next.
func (t *framedTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case res := <-t.msgCh:
		if res.err != nil {
			return nil, res.err
		}
		return json.RawMessage(res.line), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.closed:
		return nil, errors.New("mcp: transport closed")
	}
}

func (t *framedTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.closed)
		werr := t.w.Close()
		var cerr error
		if t.closeFn != nil {
			cerr = t.closeFn()
		}
		if werr != nil {
			err = werr
		} else {
			err = cerr
		}
	})
	return err
}

// NewStdioTransport launches cmd (argv form) and speaks newline-delimited
// JSON-RPC over its stdin/stdout (MCP stdio framing: one JSON object per
// line). Env entries are appended to the child's environment. The child's
// stderr is passed through to os.Stderr. Close closes the child's stdin,
// waits briefly for it to exit on its own, and kills it if it hasn't.
func NewStdioTransport(cmd []string, env []string) (Transport, error) {
	if len(cmd) == 0 {
		return nil, errors.New("mcp: NewStdioTransport: empty command")
	}

	c := exec.Command(cmd[0], cmd[1:]...)
	c.Env = append(os.Environ(), env...)
	c.Stderr = os.Stderr

	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := c.Start(); err != nil {
		return nil, err
	}

	closeFn := func() error {
		done := make(chan error, 1)
		go func() { done <- c.Wait() }()
		select {
		case err := <-done:
			return err
		case <-time.After(200 * time.Millisecond):
			_ = c.Process.Kill()
			<-done
			return nil
		}
	}

	return newFramedTransport(stdout, stdin, closeFn), nil
}
