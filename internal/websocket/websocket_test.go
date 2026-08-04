package websocket

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket/websockettest"
)

// newTestServer starts a raw TCP listener and returns it along with the
// ws:// URL clients should Dial to reach it. The listener is closed when
// the test ends.
func newTestServer(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l, "ws://" + l.Addr().String() + "/"
}

type closeInfo struct {
	code   int
	reason string
}

func dialCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// writeRawHeaderOnly writes a frame header declaring length bytes of
// payload but never writes any payload bytes — used to test that the
// client rejects an oversized/lying declared length before ever blocking
// on (or allocating for) payload it was never going to receive.
func writeRawHeaderOnly(conn net.Conn, opcode uint8, length uint64) error {
	var buf bytes.Buffer
	buf.WriteByte(0x80 | opcode) // fin=1
	buf.WriteByte(127)           // 64-bit extended length follows
	var ext [8]byte
	binary.BigEndian.PutUint64(ext[:], length)
	buf.Write(ext[:])
	_, err := conn.Write(buf.Bytes())
	return err
}

// fakeConn is a minimal net.Conn test double for whitebox tests that need
// to observe or control Write/Close/SetDeadline behavior directly, without
// a real socket. Deadline calls are tracked per axis (SetDeadline touches
// both) so tests can assert that a direction-scoped runWithContext call
// only ever touches its own axis.
type fakeConn struct {
	writeErr error
	closed   bool

	mu                 sync.Mutex
	deadlineCalls      []time.Time
	readDeadlineCalls  []time.Time
	writeDeadlineCalls []time.Time
}

func (f *fakeConn) Read(p []byte) (int, error)  { return 0, io.EOF }
func (f *fakeConn) Write(p []byte) (int, error) { return 0, f.writeErr }
func (f *fakeConn) Close() error                { f.closed = true; return nil }
func (f *fakeConn) LocalAddr() net.Addr         { return nil }
func (f *fakeConn) RemoteAddr() net.Addr        { return nil }
func (f *fakeConn) SetDeadline(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deadlineCalls = append(f.deadlineCalls, t)
	return nil
}
func (f *fakeConn) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readDeadlineCalls = append(f.readDeadlineCalls, t)
	return nil
}
func (f *fakeConn) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeDeadlineCalls = append(f.writeDeadlineCalls, t)
	return nil
}

func (f *fakeConn) snapshot() (deadline, read, write []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.deadlineCalls...),
		append([]time.Time(nil), f.readDeadlineCalls...),
		append([]time.Time(nil), f.writeDeadlineCalls...)
}

func TestDial_HandshakeSuccess(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")
}

func TestDial_WrongAccept(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Deliberately skip websockettest.Upgrade to send a bogus Accept.
		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		req.Body.Close()
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: dGhpcyBpcyB3cm9uZw==\r\n\r\n"
		conn.Write([]byte(resp))
	}()

	ctx := dialCtx(t)
	_, err := Dial(ctx, wsURL, DialOptions{})
	if err == nil {
		t.Fatal("expected error for wrong Sec-WebSocket-Accept")
	}
	if !strings.Contains(err.Error(), "Accept") {
		t.Errorf("error = %v, want mention of Accept mismatch", err)
	}
}

func TestDial_NonSwitchingProtocolsStatus(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		req.Body.Close()
		body := "not found"
		fmt.Fprintf(conn, "HTTP/1.1 404 Not Found\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	}()

	ctx := dialCtx(t)
	_, err := Dial(ctx, wsURL, DialOptions{})
	if err == nil {
		t.Fatal("expected error for non-101 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want to mention status 404", err)
	}
}

func TestEchoTextAndBinary(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			op, payload, err := websockettest.ReadMessage(conn)
			if err != nil {
				return
			}
			if err := websockettest.WriteMessage(conn, op, payload); err != nil {
				return
			}
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if err := conn.WriteText(ctx, []byte("hello")); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	mt, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != TextMessage || string(data) != "hello" {
		t.Errorf("got (%d, %q), want (%d, %q)", mt, data, TextMessage, "hello")
	}

	if err := conn.WriteBinary(ctx, []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("WriteBinary: %v", err)
	}
	mt, data, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != BinaryMessage || !bytes.Equal(data, []byte{1, 2, 3, 4}) {
		t.Errorf("got (%d, %v), want (%d, %v)", mt, data, BinaryMessage, []byte{1, 2, 3, 4})
	}
}

func TestFragmentationReassembly_TwoFragments(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.WriteFragmented(conn, websockettest.OpText, []byte("Hel"), []byte("lo!"))
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	mt, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != TextMessage || string(data) != "Hello!" {
		t.Errorf("got (%d, %q), want (%d, %q)", mt, data, TextMessage, "Hello!")
	}
}

func TestFragmentationReassembly_ThreeFragmentsInterleavedPing(t *testing.T) {
	l, wsURL := newTestServer(t)
	pongCh := make(chan []byte, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()

		websockettest.WriteFrame(conn, false, websockettest.OpText, []byte("abc"))
		websockettest.WriteFrame(conn, true, websockettest.OpPing, []byte("ping1"))
		websockettest.WriteFrame(conn, false, websockettest.OpContinuation, []byte("def"))
		websockettest.WriteFrame(conn, true, websockettest.OpContinuation, []byte("ghi"))

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpPong {
			pongCh <- payload
		} else {
			pongCh <- nil
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	mt, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != TextMessage || string(data) != "abcdefghi" {
		t.Errorf("got (%d, %q), want (%d, %q)", mt, data, TextMessage, "abcdefghi")
	}

	select {
	case payload := <-pongCh:
		if string(payload) != "ping1" {
			t.Errorf("pong payload = %q, want %q", payload, "ping1")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong")
	}
}

func TestPingAutoPong(t *testing.T) {
	l, wsURL := newTestServer(t)
	pongCh := make(chan []byte, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.WriteFrame(conn, true, websockettest.OpPing, []byte("hello-ping"))

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpPong {
			pongCh <- payload
		} else {
			pongCh <- nil
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	// Read handles the ping internally and then blocks for a data message
	// that never arrives; run it in the background purely to drive the
	// automatic pong, and let the deferred Close above unblock it at the
	// end of the test.
	go func() {
		conn.Read(context.Background())
	}()

	select {
	case payload := <-pongCh:
		if string(payload) != "hello-ping" {
			t.Errorf("pong payload = %q, want %q", payload, "hello-ping")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong")
	}
}

func TestMaskedServerFrame_ProtocolError(t *testing.T) {
	l, wsURL := newTestServer(t)
	resultCh := make(chan closeInfo, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()

		// Write a frame with the mask bit set, which is never valid from a
		// server (masking is a client-to-server requirement only).
		key := [4]byte{1, 2, 3, 4}
		payload := []byte("shouldnt work")
		masked := append([]byte(nil), payload...)
		for i := range masked {
			masked[i] ^= key[i%4]
		}
		var header bytes.Buffer
		header.WriteByte(0x80 | byte(websockettest.OpText))
		header.WriteByte(0x80 | byte(len(masked)))
		header.Write(key[:])
		header.Write(masked)
		conn.Write(header.Bytes())

		op, payload2, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose && len(payload2) >= 2 {
			resultCh <- closeInfo{code: int(binary.BigEndian.Uint16(payload2[:2])), reason: string(payload2[2:])}
		} else {
			resultCh <- closeInfo{code: -1}
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("expected error for masked server frame")
	}

	select {
	case info := <-resultCh:
		if info.code != CloseProtocolError {
			t.Errorf("server observed close code = %d, want %d", info.code, CloseProtocolError)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close echo")
	}
}

func TestOversizedMessage_ClosesWith1009(t *testing.T) {
	l, wsURL := newTestServer(t)
	resultCh := make(chan closeInfo, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		big := bytes.Repeat([]byte{'z'}, 200)
		websockettest.WriteMessage(conn, websockettest.OpText, big)

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose && len(payload) >= 2 {
			resultCh <- closeInfo{code: int(binary.BigEndian.Uint16(payload[:2]))}
		} else {
			resultCh <- closeInfo{code: -1}
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{MaxMessageBytes: 100})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("expected error for oversized message")
	}

	select {
	case info := <-resultCh:
		if info.code != 1009 {
			t.Errorf("server observed close code = %d, want 1009", info.code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close echo")
	}
}

func TestLengthBoundaries(t *testing.T) {
	for _, size := range []int{126, 65536} {
		t.Run(fmt.Sprintf("size=%d", size), func(t *testing.T) {
			l, wsURL := newTestServer(t)
			payload := bytes.Repeat([]byte{'q'}, size)
			go func() {
				conn, err := websockettest.Accept(l)
				if err != nil {
					return
				}
				defer conn.Close()
				websockettest.WriteMessage(conn, websockettest.OpBinary, payload)
			}()

			ctx := dialCtx(t)
			conn, err := Dial(ctx, wsURL, DialOptions{})
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer conn.Close(CloseNormal, "")

			mt, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if mt != BinaryMessage || !bytes.Equal(data, payload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(data), len(payload))
			}
		})
	}
}

func TestServerInitiatedClose(t *testing.T) {
	l, wsURL := newTestServer(t)
	echoCh := make(chan closeInfo, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.WriteClose(conn, CloseGoingAway, "bye")

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose && len(payload) >= 2 {
			echoCh <- closeInfo{code: int(binary.BigEndian.Uint16(payload[:2])), reason: string(payload[2:])}
		} else {
			echoCh <- closeInfo{code: -1}
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	_, _, err = conn.Read(ctx)
	var closeErr *CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("Read err = %v, want *CloseError", err)
	}
	if closeErr.Code != CloseGoingAway || closeErr.Reason != "bye" {
		t.Errorf("CloseError = %+v, want {%d bye}", closeErr, CloseGoingAway)
	}

	select {
	case info := <-echoCh:
		if info.code != CloseGoingAway {
			t.Errorf("echoed close code = %d, want %d", info.code, CloseGoingAway)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close echo")
	}

	if err := conn.Close(CloseNormal, ""); err != nil {
		t.Errorf("Close after peer-initiated close: %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(300 * time.Millisecond)
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := conn.Close(CloseNormal, "bye"); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := conn.Close(CloseNormal, "bye again"); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestCtxCancelUnblocksRead(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		time.Sleep(2 * time.Second) // hold the connection open, send nothing
	}()

	conn, err := Dial(dialCtx(t), wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	readCtx, readCancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(readCtx)
		errCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	readCancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Read err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock on ctx cancel")
	}
}

func TestDialWSS(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		accept := websockettest.ComputeAccept(r.Header.Get("Sec-WebSocket-Key"))
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
		if _, err := conn.Write([]byte(resp)); err != nil {
			return
		}

		op, payload, err := websockettest.ReadMessage(conn)
		if err != nil {
			return
		}
		websockettest.WriteMessage(conn, op, payload)
	}))
	defer server.Close()

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())

	wsURL := "wss://" + strings.TrimPrefix(server.URL, "https://") + "/"
	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{TLSConfig: &tls.Config{RootCAs: pool}})
	if err != nil {
		t.Fatalf("Dial wss: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if err := conn.WriteText(ctx, []byte("secure hello")); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	mt, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != TextMessage || string(data) != "secure hello" {
		t.Errorf("got (%d, %q), want (%d, %q)", mt, data, TextMessage, "secure hello")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server handler did not finish")
	}
}

// --- Review round: CRITICAL 1 — declared length must be checked before allocation/read ---

func TestOversizedMessage_HugeDeclaredLengthNoHangNoPanic(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		// Declare an enormous (but MSB-unset, so individually "legal")
		// length and never send any payload bytes at all.
		writeRawHeaderOnly(conn, opBinary, 1<<62)
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{MaxMessageBytes: 1024})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	readDone := make(chan error, 1)
	go func() {
		_, _, rerr := conn.Read(ctx)
		readDone <- rerr
	}()

	select {
	case rerr := <-readDone:
		if rerr == nil {
			t.Fatal("expected an error for a declared length far exceeding MaxMessageBytes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read hung instead of rejecting the declared length immediately (no panic, no allocation, no hang expected)")
	}
}

func TestOversizedMessage_JustOverBudgetNoPayloadSent(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		// Declare 101 bytes (budget is 100) and never write them.
		writeRawHeaderOnly(conn, opText, 101)
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{MaxMessageBytes: 100})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	readDone := make(chan error, 1)
	go func() {
		_, _, rerr := conn.Read(ctx)
		readDone <- rerr
	}()

	select {
	case rerr := <-readDone:
		if rerr == nil {
			t.Fatal("expected a 1009 error for a declared length just over budget")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Read hung waiting for payload bytes that were (deliberately) never sent")
	}
}

// --- Review round: CRITICAL 2 — ctx watcher must never permanently poison a healthy conn ---

func TestCtxCancelAfterSuccess_ConnStaysUsable(t *testing.T) {
	const iterations = 250

	l, wsURL := newTestServer(t)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		for i := 0; i < iterations; i++ {
			op, payload, err := websockettest.ReadMessage(conn)
			if err != nil {
				return
			}
			if err := websockettest.WriteMessage(conn, op, payload); err != nil {
				return
			}
		}
	}()

	conn, err := Dial(dialCtx(t), wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := conn.WriteText(ctx, []byte("ping")); err != nil {
			cancel()
			t.Fatalf("iteration %d: WriteText: %v", i, err)
		}
		_, data, err := conn.Read(ctx)
		// Mimic the standard `ctx, cancel := ...; defer cancel()` pattern:
		// cancel() fires immediately after the call returns successfully,
		// racing runWithContext's own internal cleanup.
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: Read: %v", i, err)
		}
		if string(data) != "ping" {
			t.Fatalf("iteration %d: got %q, want %q", i, data, "ping")
		}
	}
}

func TestRunWithContext_AlreadyDoneButFnSucceeds_ResetsDeadline(t *testing.T) {
	fc := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before runWithContext is even called

	err := runWithContext(ctx, fc, dirBoth, func() error {
		// Give the watcher goroutine time to observe the already-done ctx
		// and call SetDeadline before fn returns, deterministically
		// reproducing "ctx fires while fn is in flight but fn succeeds".
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("runWithContext returned %v, want nil (fn succeeded)", err)
	}

	deadlineCalls, _, _ := fc.snapshot()
	if len(deadlineCalls) < 2 {
		t.Fatalf("expected at least 2 SetDeadline calls (poison + reset), got %d: %v", len(deadlineCalls), deadlineCalls)
	}
	poisoned := deadlineCalls[len(deadlineCalls)-2]
	reset := deadlineCalls[len(deadlineCalls)-1]
	if !poisoned.Equal(time.Unix(0, 0)) {
		t.Errorf("expected the watcher to have set a past deadline, got %v", poisoned)
	}
	if !reset.IsZero() {
		t.Errorf("expected the deadline to be reset to the zero value since fn succeeded, got %v", reset)
	}
}

// --- Fix wave IMPORTANT 1 — runWithContext must scope deadline touches to
// one direction only, so a concurrent write's "succeeded despite ctx
// firing" cleanup (SetDeadline(time.Time{})) can never erase a concurrent
// read's poisoned deadline (or vice versa) on a full-duplex conn. Before
// the fix, both poison and reset went through the conn-global SetDeadline,
// so whichever call's cleanup ran last silently won — if that was an
// unrelated write's benign-race reset, a read that was relying on its own
// ctx-driven poison (e.g. because it's momentarily parked on writeMu for
// an automatic pong while a concurrent Send holds it) would be left
// blocked forever with no deadline and no watcher left to fix it.
func TestRunWithContext_WriteSuccessCleanup_DoesNotEraseConcurrentReadPoison(t *testing.T) {
	fc := &fakeConn{}

	readCtx, readCancel := context.WithCancel(context.Background())
	readFnEntered := make(chan struct{})
	releaseReadFn := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- runWithContext(readCtx, fc, dirRead, func() error {
			close(readFnEntered)
			<-releaseReadFn
			// Simulate the read's blocked socket call finally noticing the
			// (still-poisoned, in the fixed world) deadline and failing.
			return errors.New("simulated: interrupted by deadline")
		})
	}()

	// Let the read's fn actually start (it's "parked", analogous to being
	// blocked on writeMu for an auto-pong) before cancelling its ctx.
	<-readFnEntered
	readCancel()
	waitForCalls(t, func() int { _, reads, _ := fc.snapshot(); return len(reads) }, 1)

	// Now a concurrent Write whose own ctx has *also* already fired, but
	// whose underlying I/O nonetheless completes successfully — the
	// documented benign race already covered in isolation by
	// TestRunWithContext_AlreadyDoneButFnSucceeds_ResetsDeadline. The bug
	// here is specifically about this write's cleanup crossing over onto
	// the read's axis.
	writeCtx, writeCancel := context.WithCancel(context.Background())
	writeCancel()
	if err := runWithContext(writeCtx, fc, dirWrite, func() error {
		// Give the watcher goroutine time to observe the already-done ctx
		// before fn returns, deterministically reproducing "ctx fires
		// while fn is in flight but fn succeeds" (same technique as
		// TestRunWithContext_AlreadyDoneButFnSucceeds_ResetsDeadline).
		time.Sleep(20 * time.Millisecond)
		return nil // "succeeded despite ctx firing"
	}); err != nil {
		t.Fatalf("write runWithContext = %v, want nil (fn succeeded)", err)
	}

	// Let the parked read fn finish and observe the overall result.
	close(releaseReadFn)
	if err := <-readDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("read runWithContext = %v, want context.Canceled", err)
	}

	_, readCalls, writeCalls := fc.snapshot()
	if len(readCalls) != 1 || !readCalls[0].Equal(time.Unix(0, 0)) {
		t.Fatalf("read deadline calls = %v, want exactly one poison (past); the write's cleanup must not have touched it", readCalls)
	}
	if len(writeCalls) == 0 || !writeCalls[len(writeCalls)-1].IsZero() {
		t.Fatalf("write deadline calls = %v, want to end with a reset (zero value)", writeCalls)
	}
}

// waitForCalls polls get() until it returns >= want, failing the test if
// that doesn't happen within a short bound (used to deterministically wait
// for a background watcher goroutine to have run, without a hangable
// unconditional wait).
func waitForCalls(t *testing.T, get func() int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if get() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d call(s), got %d", want, get())
}

// --- Review round: IMPORTANT 1 — a protocolErr from readFrameHeader must abort (1002 + close socket) ---

func TestProtocolViolation_ClosesSocketAndSends1002(t *testing.T) {
	l, wsURL := newTestServer(t)
	resultCh := make(chan closeInfo, 1)
	eofCh := make(chan bool, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()

		// A 126-byte ping is a direct RFC 6455 §5.5 violation (control
		// frames must be <=125 bytes) surfaced as a protocolErr from
		// readFrameHeader.
		websockettest.WriteFrame(conn, true, websockettest.OpPing, bytes.Repeat([]byte{'p'}, 126))

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose && len(payload) >= 2 {
			resultCh <- closeInfo{code: int(binary.BigEndian.Uint16(payload[:2]))}
		} else {
			resultCh <- closeInfo{code: -1}
		}

		// Confirm the client actually closed its socket (no fd leak)
		// rather than merely returning an error while leaving the TCP
		// connection half-open. Depending on OS-level timing, tearing
		// down a connection can surface as a clean io.EOF or as a
		// "connection reset by peer" (both are legitimate outcomes of the
		// peer having torn down the socket); only a timeout indicates the
		// fd was never actually closed.
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, rerr := conn.Read(b[:])
		var netErr net.Error
		isTimeout := errors.As(rerr, &netErr) && netErr.Timeout()
		eofCh <- rerr != nil && !isTimeout
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("expected an error for the oversized ping")
	}

	select {
	case info := <-resultCh:
		if info.code != CloseProtocolError {
			t.Errorf("server observed close code = %d, want %d", info.code, CloseProtocolError)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the 1002 close echo")
	}

	select {
	case torndown := <-eofCh:
		if !torndown {
			t.Error("expected the client to have torn down its socket (server should observe EOF or reset, not a timeout); the fd may be leaked")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting to observe the client's socket closure")
	}
}

// --- Review round: IMPORTANT 2 — never echo an explicit 1005 on the wire ---

func TestServerInitiatedClose_EmptyPayloadEchoedEmpty(t *testing.T) {
	l, wsURL := newTestServer(t)
	echoCh := make(chan []byte, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		// No status code at all — an entirely empty close payload.
		websockettest.WriteFrame(conn, true, websockettest.OpClose, nil)

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose {
			echoCh <- payload
		} else {
			echoCh <- []byte("READ-ERROR")
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	_, _, err = conn.Read(ctx)
	var closeErr *CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("Read err = %v, want *CloseError", err)
	}
	if closeErr.Code != closeNoStatus {
		t.Errorf("CloseError.Code = %d, want %d (no status code received)", closeErr.Code, closeNoStatus)
	}

	select {
	case echoed := <-echoCh:
		if len(echoed) != 0 {
			t.Errorf("echoed close payload = %v (len %d), want empty: RFC 6455 §7.4.1 forbids ever sending 1005 on the wire", echoed, len(echoed))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the close echo")
	}

	conn.Close(CloseNormal, "")
}

// --- Fix wave MINOR 4 — a close frame with exactly a 1-byte payload is not
// a valid RFC 6455 §7.4.1 encoding (0 bytes, or >=2 bytes for a status
// code) and must abort with 1002 rather than being echoed back. ---

func TestServerInitiatedClose_OneBytePayloadAborts(t *testing.T) {
	l, wsURL := newTestServer(t)
	resultCh := make(chan closeInfo, 1)
	go func() {
		conn, err := websockettest.Accept(l)
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.WriteFrame(conn, true, websockettest.OpClose, []byte{0x03})

		op, payload, err := websockettest.ReadMessage(conn)
		if err == nil && op == websockettest.OpClose && len(payload) >= 2 {
			resultCh <- closeInfo{code: int(binary.BigEndian.Uint16(payload[:2]))}
		} else {
			resultCh <- closeInfo{code: -1}
		}
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("expected an error for the 1-byte close payload")
	}

	select {
	case info := <-resultCh:
		if info.code != CloseProtocolError {
			t.Errorf("server observed close code = %d, want %d (never echoed)", info.code, CloseProtocolError)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the 1002 abort")
	}
}

// --- Review round: MINOR (b) — a failed automatic pong write must shut down the conn ---

func TestPingAutoPongWriteFailureShutsDownConn(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, true, opPing, []byte("hi"), nil); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	fc := &fakeConn{writeErr: errors.New("boom")}
	c := &Conn{conn: fc, br: bufio.NewReader(&buf), maxMessage: defaultMaxMessageBytes}

	if _, _, err := c.readMessage(); err == nil {
		t.Fatal("expected an error when the automatic pong write fails")
	}
	if !fc.closed {
		t.Error("expected the connection to be shut down after a failed automatic pong write")
	}
}

// --- Task 6 — control-frame writes (the automatic pong Read sends for a
// ping) must carry a bounded write deadline, so a peer that pings and then
// stops draining can't wedge Read forever on the reply. ---

func TestWriteControlSetsBoundedWriteDeadline(t *testing.T) {
	fc := &fakeConn{}
	c := &Conn{conn: fc}

	if err := c.writeControl(opPong, []byte("pong-payload")); err != nil {
		t.Fatalf("writeControl: %v", err)
	}

	_, _, writeDeadlines := fc.snapshot()
	if len(writeDeadlines) < 2 {
		t.Fatalf("expected at least 2 SetWriteDeadline calls (set before writing, clear after), got %d: %v", len(writeDeadlines), writeDeadlines)
	}

	first := writeDeadlines[0]
	if first.IsZero() {
		t.Fatal("expected writeControl to set a non-zero write deadline before writing the frame")
	}
	if d := time.Until(first); d <= 0 || d > controlWriteTimeout+time.Second {
		t.Fatalf("write deadline %v from now, want within (0, %v]", d, controlWriteTimeout)
	}

	last := writeDeadlines[len(writeDeadlines)-1]
	if !last.IsZero() {
		t.Fatalf("expected writeControl to clear the write deadline after writing (so it doesn't leak into a later WriteText/WriteBinary call), got %v", last)
	}
}

// --- Review round: MINOR (c) — reserved headers are skipped; CR/LF is rejected ---

func TestDial_HeaderCRLFRejected(t *testing.T) {
	l, wsURL := newTestServer(t)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// The malformed header must be caught before any bytes are sent,
		// so there's nothing for the server to do here except not hang.
	}()

	ctx := dialCtx(t)
	_, err := Dial(ctx, wsURL, DialOptions{Header: http.Header{"X-Evil": {"value\r\nInjected: true"}}})
	if err == nil {
		t.Fatal("expected an error for a header value containing CR/LF")
	}
	if !strings.Contains(err.Error(), "CR or LF") {
		t.Errorf("error = %v, want to mention CR or LF", err)
	}
}

func TestDial_ReservedHeadersSkipped(t *testing.T) {
	l, wsURL := newTestServer(t)
	reqCh := make(chan *http.Request, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			reqCh <- nil
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		req, err := http.ReadRequest(br)
		if err != nil {
			reqCh <- nil
			return
		}
		req.Body.Close()
		reqCh <- req

		accept := websockettest.ComputeAccept(req.Header.Get("Sec-WebSocket-Key"))
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
		conn.Write([]byte(resp))
	}()

	ctx := dialCtx(t)
	conn, err := Dial(ctx, wsURL, DialOptions{Header: http.Header{
		"Host":                     {"evil.example"},
		"Sec-WebSocket-Extensions": {"permessage-deflate"},
		"X-Custom":                 {"value1"},
	}})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(CloseNormal, "")

	req := <-reqCh
	if req == nil {
		t.Fatal("server did not receive a request")
	}
	if req.Host == "evil.example" {
		t.Errorf("Host header was overridden by caller-supplied header: %q", req.Host)
	}
	if got := req.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Errorf("Sec-WebSocket-Extensions = %q, want empty (reserved headers must be skipped)", got)
	}
	if got := req.Header.Get("X-Custom"); got != "value1" {
		t.Errorf("X-Custom = %q, want %q (non-reserved headers must pass through)", got, "value1")
	}
}

// --- Task 3 — a blocked WriteText/WriteBinary must not clobber an
// in-flight control write it's merely queued behind on writeMu ---

// blockingConn is a net.Conn test double whose Write blocks until either
// releaseWrite is closed (simulating the write finally completing) or the
// most recently set write deadline (tracked via the embedded fakeConn) has
// passed — mimicking how a real net.Conn aborts a blocked Write once
// SetWriteDeadline is called with a time in the past. writeStarted is
// closed the first time Write is entered, letting a test synchronize with
// the write becoming genuinely in-flight.
type blockingConn struct {
	*fakeConn
	writeStarted chan struct{}
	releaseWrite chan struct{}
	startOnce    sync.Once
}

func (b *blockingConn) Write(p []byte) (int, error) {
	b.startOnce.Do(func() { close(b.writeStarted) })
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-b.releaseWrite:
			return len(p), nil
		case <-ticker.C:
			_, _, writeDeadlines := b.fakeConn.snapshot()
			if len(writeDeadlines) == 0 {
				continue
			}
			last := writeDeadlines[len(writeDeadlines)-1]
			if !last.IsZero() && time.Now().After(last) {
				return 0, errors.New("i/o timeout (simulated deadline exceeded)")
			}
		}
	}
}

func TestWriteMu_BlockedWriterCtxCancelDoesNotClobberInFlightControlWrite(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	bc := &blockingConn{fakeConn: &fakeConn{}, writeStarted: writeStarted, releaseWrite: releaseWrite}
	c := &Conn{conn: bc}

	// Start a control write (as Read's auto-pong path does) and wait for
	// it to actually be in flight, holding writeMu.
	controlDone := make(chan error, 1)
	go func() {
		controlDone <- c.writeControl(opPong, []byte("pong-payload"))
	}()

	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("control write never started")
	}

	// Now queue a WriteText behind it. It must block on writeMu (the
	// control write holds it), then have its ctx cancelled while still
	// waiting.
	ctx, cancel := context.WithCancel(context.Background())
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- c.writeMessage(ctx, opText, []byte("hello"))
	}()

	time.Sleep(100 * time.Millisecond) // let the WriteText goroutine block on writeMu
	cancel()

	select {
	case err := <-writeDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked writeMessage returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked writeMessage did not return promptly after its ctx was cancelled")
	}

	// The control write must still be genuinely in flight and untouched —
	// release it now and confirm it completes successfully rather than
	// having been aborted by a deadline the cancelled writer set while it
	// was only waiting on writeMu, not actually writing.
	close(releaseWrite)

	select {
	case err := <-controlDone:
		if err != nil {
			t.Fatalf("in-flight control write was aborted by the blocked writer's ctx cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("control write never completed")
	}
}

// TestWriteMu_InFlightWriteStillInterruptedByOwnCtx confirms the fix didn't
// remove ctx-cancellation for a write that's genuinely in flight (owns
// writeMu, is blocked in conn.Write): that case must still be interrupted
// promptly via the deadline, per Read/Write's documented contract.
func TestWriteMu_InFlightWriteStillInterruptedByOwnCtx(t *testing.T) {
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	defer close(releaseWrite) // let the goroutine's Write eventually return, even after the deadline fires
	bc := &blockingConn{fakeConn: &fakeConn{}, writeStarted: writeStarted, releaseWrite: releaseWrite}
	c := &Conn{conn: bc}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- c.writeMessage(ctx, opText, []byte("hello"))
	}()

	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("write never started")
	}

	cancel() // this write owns writeMu and is genuinely in flight: must be interrupted

	select {
	case err := <-writeDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight writeMessage returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight writeMessage was not interrupted by its own ctx cancellation")
	}
}
