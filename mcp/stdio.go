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

	writeMu sync.Mutex
	w       io.WriteCloser

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
	_, err := t.w.Write(line)
	return err
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
