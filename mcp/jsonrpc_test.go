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

// TestServerDispatchBoundedConcurrency pins the MED fix: a burst of
// server-initiated requests must never have more than
// maxConcurrentServerDispatch dispatch goroutines running at once. Requests
// that arrive while the bound is saturated must be rejected immediately
// with a JSON-RPC -32603 "server busy" reply (not queued, not blocking
// recvLoop) rather than spawning past the cap.
func TestServerDispatchBoundedConcurrency(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	release := make(chan struct{})
	var concurrent int32
	var maxConcurrent int32
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		n := atomic.AddInt32(&concurrent, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if n <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxConcurrent, old, n) {
				break
			}
		}
		<-release
		atomic.AddInt32(&concurrent, -1)
		return ElicitationResult{Action: "decline"}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	const burst = 20
	for i := 0; i < burst; i++ {
		sendServerRequest(t, server, int64(i), "elicitation/create", map[string]any{"message": "x"})
	}

	// recvLoop processes the burst strictly in order: the first
	// maxConcurrentServerDispatch requests acquire the dispatch semaphore
	// and block their goroutines on release (so they send nothing yet); the
	// rest find the semaphore saturated and get a synchronous "server busy"
	// reply without waiting for release. So the first
	// burst-maxConcurrentServerDispatch responses observed here must all be
	// busy errors.
	const wantBusy = burst - maxConcurrentServerDispatch
	for i := 0; i < wantBusy; i++ {
		resp := recvServerResponse(t, server)
		if resp.Error == nil {
			t.Fatalf("response %d: Error = nil, want -32603 server-busy", i)
		}
		if resp.Error.Code != rpcInternalError {
			t.Fatalf("response %d: Error.Code = %d, want %d", i, resp.Error.Code, rpcInternalError)
		}
	}

	if m := atomic.LoadInt32(&maxConcurrent); m > maxConcurrentServerDispatch {
		t.Fatalf("observed max concurrent dispatch = %d, want <= %d", m, maxConcurrentServerDispatch)
	}

	close(release)

	for i := 0; i < maxConcurrentServerDispatch; i++ {
		resp := recvServerResponse(t, server)
		if resp.Error != nil {
			t.Fatalf("accepted request got error reply: %+v", resp.Error)
		}
	}
}

// TestCloseDrainsDispatchGoroutines pins the Close-drain half of the MED
// fix: Close must not return until every in-flight server-request dispatch
// goroutine has actually finished, not merely been asked (via ctx
// cancellation) to finish. A handler that only exits on ctx.Done() lets this
// test tell "Close returned" apart from "the goroutine is done" — without
// dispatchWG.Wait() in Close, the two events race and the goroutine may
// still be running (and could still call transport.Send) after Close
// returns.
func TestCloseDrainsDispatchGoroutines(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)

	entered := make(chan struct{})
	var handlerDone int32
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		close(entered)
		<-ctx.Done() // only unblocks once Close cancels the client's ctx
		atomic.StoreInt32(&handlerDone, 1)
		return ElicitationResult{}, ctx.Err()
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	sendServerRequest(t, server, 1, "elicitation/create", map[string]any{"message": "x"})

	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("dispatch goroutine never started")
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if atomic.LoadInt32(&handlerDone) != 1 {
		t.Fatal("Close returned before the in-flight dispatch goroutine finished (dispatchWG not drained)")
	}
}

// TestCloseReturnsWithinGraceWhenHandlerIgnoresCtx pins the bounded-drain
// fix: a misbehaving ElicitationHandler that ignores ctx and blocks forever
// (on an unrelated channel that's never closed, simulating a stuck UI
// prompt or downstream call) must not make Close hang forever. Close must
// still return, within closeDrainGrace plus a small scheduling margin.
func TestCloseReturnsWithinGraceWhenHandlerIgnoresCtx(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)

	entered := make(chan struct{})
	neverClosed := make(chan struct{}) // deliberately never closed
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		close(entered)
		<-neverClosed // ignores ctx entirely — the misbehavior under test
		return ElicitationResult{}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	sendServerRequest(t, server, 1, "elicitation/create", map[string]any{"message": "x"})

	select {
	case <-entered:
	case <-time.After(testTimeout):
		t.Fatal("dispatch goroutine never started")
	}

	closeDone := make(chan error, 1)
	start := time.Now()
	go func() { closeDone <- c.Close() }()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		if elapsed := time.Since(start); elapsed > closeDrainGrace+2*time.Second {
			t.Fatalf("Close took %v, want roughly within closeDrainGrace (%v)", elapsed, closeDrainGrace)
		}
	case <-time.After(closeDrainGrace + 2*time.Second):
		t.Fatal("Close did not return within the drain grace bound — a ctx-ignoring handler hung it")
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
