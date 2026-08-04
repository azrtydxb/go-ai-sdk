package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// TestBusyReplyDropsWithoutWedgingRecvLoop pins the busy-reply bound: when
// the saturated-dispatch "server busy" reply (respondServerError, called
// synchronously from recvLoop — see busyReplyTimeout) can't acquire sendSem
// within ~busyReplyTimeout (simulating a stuck write on a non-self-serializing
// transport holding it, e.g. a hung stdio child), it must give up and drop
// the reply rather than block recvLoop indefinitely. pipeTransport does not
// implement selfSerializingTransport, so this exercises exactly the
// sendSem-gated path stdio uses.
func TestBusyReplyDropsWithoutWedgingRecvLoop(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()
	if c.selfSerializes {
		t.Fatal("Client.selfSerializes must be false for pipeTransport")
	}

	release := make(chan struct{})
	c.SetElicitationHandler(func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error) {
		<-release
		return ElicitationResult{Action: "decline"}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"elicitation": map[string]any{}})

	// Saturate the dispatch bound so the next server request hits recvLoop's
	// "busy" branch.
	for i := 0; i < maxConcurrentServerDispatch; i++ {
		sendServerRequest(t, server, int64(i), "elicitation/create", map[string]any{"message": "x"})
	}
	// Give recvLoop a moment to actually acquire all dispatchSem slots
	// before the overflow request is sent.
	time.Sleep(50 * time.Millisecond)

	// Simulate a stuck write (e.g. a wedged stdio child that isn't draining
	// stdin) by holding sendSem ourselves — exactly what an in-progress Send
	// would do — and deliberately never releasing it in this phase of the
	// test.
	c.sendSem.Lock()

	sendServerRequest(t, server, 1000, "elicitation/create", map[string]any{"message": "overflow-1"})

	// No busy reply should arrive quickly: its sendSem acquisition is
	// bounded to ~busyReplyTimeout, well short of "block forever", so
	// nothing shows up yet.
	tooSoonCtx, cancel := context.WithTimeout(context.Background(), busyReplyTimeout/2)
	if _, err := server.Receive(tooSoonCtx); err == nil {
		cancel()
		t.Fatal("busy reply arrived before the sendSem timeout elapsed")
	}
	cancel()

	// Wait past busyReplyTimeout: the dropped-reply attempt must have given
	// up by now. Prove recvLoop itself kept looping (was not wedged trying
	// to send that first reply) by releasing the semaphore and sending a
	// second overflow request — if recvLoop were stuck, this would never
	// even be read off the transport, let alone answered.
	time.Sleep(busyReplyTimeout + 100*time.Millisecond)
	c.sendSem.Unlock()

	start := time.Now()
	sendServerRequest(t, server, 1001, "elicitation/create", map[string]any{"message": "overflow-2"})
	resp := recvServerResponse(t, server)
	if resp.Error == nil || resp.Error.Code != rpcInternalError {
		t.Fatalf("second overflow request: got %+v, want a prompt -32603 busy reply proving recvLoop kept looping", resp)
	}
	if elapsed := time.Since(start); elapsed > testTimeout {
		t.Fatalf("recvLoop took %v to answer after recovering, want it prompt", elapsed)
	}

	close(release)
	for i := 0; i < maxConcurrentServerDispatch; i++ {
		recvServerResponse(t, server)
	}
}

// concurrencyTrackingTransport is a minimal in-memory Transport whose Send
// records how many goroutines are inside it at once (via an atomic
// increment-sleep-decrement around the body), tracking the maximum observed
// concurrency. It does not implement selfSerializingTransport, so a Client
// built on it must serialize Send itself if the sendSem gating in
// Client.call is doing its job. Used by
// TestClientSerializesSendForNonSelfSerializingTransport and (wrapped by
// selfSerializingConcurrencyTransport) by
// TestClientDoesNotSerializeSendForSelfSerializingTransport — together they
// pin the actual branch behavior of the `!c.selfSerializes` gate, which
// framedTransport-over-io.Pipe cannot: framedTransport has its own internal
// writeMu that would serialize writes regardless of whether Client holds
// sendSem, so a test built only on framedTransport can't distinguish
// "Client serialized it" from "the transport would have serialized it
// anyway."
type concurrencyTrackingTransport struct {
	recv chan json.RawMessage

	cur int32
	max int32
}

func newConcurrencyTrackingTransport() *concurrencyTrackingTransport {
	return &concurrencyTrackingTransport{recv: make(chan json.RawMessage, 64)}
}

func (t *concurrencyTrackingTransport) Send(ctx context.Context, msg json.RawMessage) error {
	n := atomic.AddInt32(&t.cur, 1)
	for {
		old := atomic.LoadInt32(&t.max)
		if n <= old || atomic.CompareAndSwapInt32(&t.max, old, n) {
			break
		}
	}
	// Hold the "in Send" window open long enough that concurrent Send calls,
	// if the Client allowed them, would reliably overlap here rather than
	// racing past each other before either goroutine records its entry.
	time.Sleep(20 * time.Millisecond)
	atomic.AddInt32(&t.cur, -1)

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(msg, &req); err != nil {
		return err
	}
	resp := json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, req.ID))
	select {
	case t.recv <- resp:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *concurrencyTrackingTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case m := <-t.recv:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *concurrencyTrackingTransport) Close() error { return nil }

// selfSerializingConcurrencyTransport wraps concurrencyTrackingTransport and
// additionally implements selfSerializingTransport, reporting true — the
// control case proving the gate in Client.call actually branches on the
// capability rather than e.g. always serializing regardless of it.
type selfSerializingConcurrencyTransport struct {
	*concurrencyTrackingTransport
}

func (t *selfSerializingConcurrencyTransport) SelfSerializes() bool { return true }

// runConcurrentCalls fires n Client.call invocations from n goroutines,
// released together via a shared start barrier so they race Send as
// closely to simultaneously as goroutine scheduling allows, then waits for
// all of them to complete.
func runConcurrentCalls(t *testing.T, c *Client, n int) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
			defer cancel()
			if _, err := c.call(ctx, fmt.Sprintf("m-%d", i), nil); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
}

// TestClientSerializesSendForNonSelfSerializingTransport pins the load-
// bearing half of the sendSem gate in Client.call: for a Transport that does
// NOT implement selfSerializingTransport (the exported Transport interface's
// documented at-most-one-writer contract for third-party transports, which
// stdio's framedTransport also relies on), Client must never have more than
// one Send in flight at a time, regardless of how many concurrent calls are
// made. A regression that dropped or misapplied the `!c.selfSerializes`
// guard would let this fail (max observed concurrency > 1).
func TestClientSerializesSendForNonSelfSerializingTransport(t *testing.T) {
	tr := newConcurrencyTrackingTransport()
	c := NewClient(tr)
	defer c.Close()
	if c.selfSerializes {
		t.Fatal("Client.selfSerializes = true for a transport that doesn't implement selfSerializingTransport")
	}

	runConcurrentCalls(t, c, 10)

	if max := atomic.LoadInt32(&tr.max); max != 1 {
		t.Fatalf("max observed concurrent Send = %d, want 1 (Client must serialize Send for a non-self-serializing transport)", max)
	}
}

// TestClientDoesNotSerializeSendForSelfSerializingTransport is the control
// for the test above: with the same underlying transport but wrapped to
// report SelfSerializes() == true, Client must NOT hold sendSem across
// Send, so concurrent calls really do overlap inside Send. Without this
// control, a version of Client.call that (incorrectly) always serializes
// regardless of c.selfSerializes would still pass the non-self-serializing
// test above for the wrong reason.
func TestClientDoesNotSerializeSendForSelfSerializingTransport(t *testing.T) {
	inner := newConcurrencyTrackingTransport()
	tr := &selfSerializingConcurrencyTransport{inner}
	c := NewClient(tr)
	defer c.Close()
	if !c.selfSerializes {
		t.Fatal("Client.selfSerializes = false for a transport implementing selfSerializingTransport that returns true")
	}

	runConcurrentCalls(t, c, 10)

	if max := atomic.LoadInt32(&inner.max); max <= 1 {
		t.Fatalf("max observed concurrent Send = %d, want > 1 (Client must NOT serialize Send for a self-serializing transport)", max)
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
