package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// erroringTransport is a Transport whose Receive fails immediately (as if
// the underlying connection died) and which counts Close calls, so tests
// can assert Close was actually invoked (fix #2) without depending on any
// real subprocess or network I/O.
type erroringTransport struct {
	recvErr    error
	closeCalls int32
	sendCh     chan struct{} // closed on first Send, if non-nil
}

func (t *erroringTransport) Send(ctx context.Context, msg json.RawMessage) error {
	if t.sendCh != nil {
		select {
		case <-t.sendCh:
		default:
			close(t.sendCh)
		}
	}
	<-ctx.Done() // block until the caller gives up, like a wedged transport
	return ctx.Err()
}

func (t *erroringTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	return nil, t.recvErr
}

func (t *erroringTransport) Close() error {
	atomic.AddInt32(&t.closeCalls, 1)
	return nil
}

// TestRecvErrorClosesTransport pins fix #2: a transport read error reaching
// recvLoop must cause the Client to close the transport (idempotently),
// not just cancel its internal ctx. Before the fix, closeWith cancelled
// c.ctx but never called transport.Close(), leaking a zombie child process
// (for the real stdio transport) and a parked reader goroutine forever.
func TestRecvErrorClosesTransport(t *testing.T) {
	tr := &erroringTransport{recvErr: errors.New("simulated read error")}
	c := NewClient(tr)
	defer c.Close()

	// recvLoop runs on its own goroutine and hits the read error almost
	// immediately; wait for the client to observe closure rather than
	// racing a fixed sleep.
	select {
	case <-c.loopDone:
	case <-time.After(testTimeout):
		t.Fatal("client did not close after a transport read error")
	}

	if calls := atomic.LoadInt32(&tr.closeCalls); calls < 1 {
		t.Fatalf("transport.Close was called %d times, want at least 1", calls)
	}
	if c.closeErr == nil || !strings.Contains(c.closeErr.Error(), "simulated read error") {
		t.Fatalf("closeErr = %v, want it to wrap the transport's read error", c.closeErr)
	}
}

// TestCloseIsIdempotentOnTransport asserts Client.Close, called after the
// recvLoop error path already closed the transport once (fix #2), does not
// somehow double-fail or panic — transport.Close is documented as required
// to be idempotent, and Client relies on that.
func TestCloseIsIdempotentOnTransport(t *testing.T) {
	tr := &erroringTransport{recvErr: errors.New("boom")}
	c := NewClient(tr)

	select {
	case <-c.loopDone:
	case <-time.After(testTimeout):
		t.Fatal("client did not close after a transport read error")
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls := atomic.LoadInt32(&tr.closeCalls); calls != 1 {
		t.Fatalf("transport.Close was called %d times, want exactly 1 (closeOnce)", calls)
	}
}

// TestCallAfterDeathReturnsCloseErr pins fix #6: once the client is closed,
// a new call() must fail with the real closure cause (c.closeErr), not the
// generic errClosedMsg placeholder that discards it.
func TestCallAfterDeathReturnsCloseErr(t *testing.T) {
	tr := &erroringTransport{recvErr: errors.New("the actual root cause")}
	c := NewClient(tr)
	defer c.Close()

	select {
	case <-c.loopDone:
	case <-time.After(testTimeout):
		t.Fatal("client did not close after a transport read error")
	}

	_, err := c.call(context.Background(), "tools/list", nil)
	if err == nil {
		t.Fatal("call after close: want error, got nil")
	}
	if !strings.Contains(err.Error(), "the actual root cause") {
		t.Fatalf("call after close err = %v, want it to contain the real closure cause", err)
	}
}

// TestNextIDIsAtomicInt64 is a light sanity check that concurrent calls
// each get a unique, monotonically-assigned id via atomic.Int64 (fix #5) —
// mostly a compile-time/API check (the alignment issue it fixes is a
// 32-bit-only concern this test can't reproduce on a 64-bit CI host), but
// it does exercise concurrent Add calls for a data race under -race.
func TestNextIDIsAtomicInt64(t *testing.T) {
	c := &Client{}
	const n = 100
	seen := make(chan int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen <- c.nextID.Add(1)
		}()
	}
	wg.Wait()
	close(seen)

	ids := make(map[int64]bool, n)
	for id := range seen {
		if ids[id] {
			t.Fatalf("duplicate id %d", id)
		}
		ids[id] = true
	}
	if len(ids) != n {
		t.Fatalf("got %d unique ids, want %d", len(ids), n)
	}
}
