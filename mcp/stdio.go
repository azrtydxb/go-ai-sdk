package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// maxLineSize bounds a single newline-delimited JSON-RPC message (mirrors
// internal/sse's line limit).
const maxLineSize = 10 * 1024 * 1024

// framedTransport speaks newline-delimited JSON-RPC over an io.Reader /
// io.WriteCloser pair: one JSON object per line, in each direction. It is
// the framing logic behind NewStdioTransport, kept independent of exec.Cmd
// so it can be tested over plain pipes.
type framedTransport struct {
	scanner *bufio.Scanner

	writeMu sync.Mutex
	w       io.WriteCloser

	closeOnce sync.Once
	closeFn   func() error
}

// newFramedTransport builds a Transport that reads newline-delimited JSON
// from r and writes newline-delimited JSON to w. closeFn is invoked exactly
// once by Close, after w has been closed, to release any additional
// resources (e.g. terminate a child process).
func newFramedTransport(r io.Reader, w io.WriteCloser, closeFn func() error) *framedTransport {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return &framedTransport{scanner: sc, w: w, closeFn: closeFn}
}

func (t *framedTransport) Send(ctx context.Context, msg json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line := make([]byte, 0, len(msg)+1)
	line = append(line, msg...)
	line = append(line, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := t.w.Write(line)
	return err
}

// Receive returns the next line as a JSON-RPC message. The scan happens in
// a goroutine so that ctx cancellation can unblock the caller even though
// bufio.Scanner offers no native cancellation; the goroutine outlives a
// cancelled Receive call and exits once the underlying reader unblocks
// (Close terminates the process, which does that).
func (t *framedTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		if t.scanner.Scan() {
			b := t.scanner.Bytes()
			cp := make([]byte, len(b))
			copy(cp, b)
			ch <- result{line: cp}
			return
		}
		err := t.scanner.Err()
		if err == nil {
			err = io.EOF
		}
		ch <- result{err: err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return json.RawMessage(res.line), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *framedTransport) Close() error {
	var err error
	t.closeOnce.Do(func() {
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
