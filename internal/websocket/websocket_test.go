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
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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
