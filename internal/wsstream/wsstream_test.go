package wsstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket"
	"github.com/azrtydxb/go-ai-sdk/internal/websocket/websockettest"
)

// testEvent is the event type used by these generic-level tests.
type testEvent struct {
	Text string
}

// listenerBaseURL starts a TCP listener and returns it plus the "http://"
// base URL DialURL derives a ws:// dial URL from.
func listenerBaseURL(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l, "http://" + l.Addr().String()
}

// dialClient dials a *websocket.Conn against a listener's ws:// address,
// deriving it via DialURL the same way every provider does.
func dialClient(t *testing.T, baseURL string) *websocket.Conn {
	t.Helper()
	u, err := DialURL(baseURL, "/v1/stream")
	if err != nil {
		t.Fatalf("DialURL: %v", err)
	}
	conn, err := websocket.Dial(context.Background(), u.String(), websocket.DialOptions{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return conn
}

// echoDecode treats every text message as one testEvent carrying the raw
// payload, "TERMINAL" as a clean terminal message, and "ERROR" as a decode
// error. Binary messages are ignored (mirrors how deepgram's Decode ignores
// message types it doesn't care about).
func echoDecode(mt int, data []byte) ([]testEvent, bool, error) {
	if mt != websocket.TextMessage {
		return nil, false, nil
	}
	switch string(data) {
	case "TERMINAL":
		return nil, true, nil
	case "ERROR":
		return nil, false, errors.New("echoDecode: boom")
	case "":
		return nil, false, nil
	default:
		return []testEvent{{Text: string(data)}}, false, nil
	}
}

func TestSend_TextAndBinaryRouting(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	frameCh := make(chan struct {
		opcode  int
		payload []byte
	}, 2)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		for i := 0; i < 2; i++ {
			opcode, payload, err := websockettest.ReadMessage(conn)
			if err != nil {
				return
			}
			frameCh <- struct {
				opcode  int
				payload []byte
			}{opcode, payload}
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	defer s.Close()

	if err := s.Send(context.Background(), websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("Send text: %v", err)
	}
	if err := s.Send(context.Background(), websocket.BinaryMessage, []byte("bin")); err != nil {
		t.Fatalf("Send binary: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case f := <-frameCh:
			switch i {
			case 0:
				if f.opcode != websockettest.OpText || string(f.payload) != "hello" {
					t.Fatalf("frame 0 = opcode=%d payload=%q, want text %q", f.opcode, f.payload, "hello")
				}
			case 1:
				if f.opcode != websockettest.OpBinary || string(f.payload) != "bin" {
					t.Fatalf("frame 1 = opcode=%d payload=%q, want binary %q", f.opcode, f.payload, "bin")
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for frame %d", i)
		}
	}

	for range s.Events() {
	}
}

func TestDecodeRouting_EventsDelivered(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("one"))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("")) // skipped
		websockettest.WriteMessage(conn, websockettest.OpBinary, []byte("ignored-binary"))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("two"))
		websockettest.WriteClose(conn, 1000, "")
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	defer s.Close()

	var got []testEvent
	for e := range s.Events() {
		got = append(got, e)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("got %+v, want [one two]", got)
	}
}

func TestTerminalFlag_EndsCleanly(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("one"))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("TERMINAL"))
		// A message after the terminal one must never be observed — the
		// readLoop must have already returned.
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("should-not-arrive"))
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	defer s.Close()

	var got []testEvent
	for e := range s.Events() {
		got = append(got, e)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil after a terminal Decode result", err)
	}
	if len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("got %+v, want [one]", got)
	}
}

func TestDecodeError_EndsWithThatErr(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("ERROR"))
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	defer s.Close()

	for range s.Events() {
	}
	if s.Err() == nil {
		t.Fatal("Err() = nil, want the decode error")
	}
}

func TestClose_Idempotent(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.Upgrade(conn)
		websockettest.ReadMessage(conn)
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close (2nd): %v", err)
	}
	if !s.Closed() {
		t.Fatal("Closed() = false after Close()")
	}

	for range s.Events() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil after explicit Close", err)
	}
}

func TestSend_AfterClose(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.Upgrade(conn)
		websockettest.ReadMessage(conn)
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Send(context.Background(), websocket.TextMessage, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after Close: err = %v, want ErrClosed", err)
	}

	for range s.Events() {
	}
}

// TestAbandonedEventsThenCloseUnblocksReadLoop pins the leak fix that
// motivated centralizing this machinery: readLoop's per-event send must not
// block forever once a consumer stops ranging over Events() (without
// cancelling ctx) — Close() must unblock it via the closeCh case in the
// select.
func TestAbandonedEventsThenCloseUnblocksReadLoop(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		// Flood far more messages than the default event buffer (32) so
		// the reader goroutine is guaranteed to still be blocked trying to
		// deliver one when the consumer below abandons Events().
		for i := 0; i < 200; i++ {
			websockettest.WriteMessage(conn, websockettest.OpText, []byte(fmt.Sprintf("msg-%d", i)))
		}
		// Keep the connection open; the client side closes it via Close().
		websockettest.ReadMessage(conn)
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})

	n := 0
	for range s.Events() {
		n++
		if n == 3 {
			break
		}
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-s.Done():
		// reader goroutine exited, as required.
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop leaked: did not exit within 2s of Close() after Events() was abandoned")
	}
}

func TestReadLoop_ClosesUnderlyingConnOnTerminal(t *testing.T) {
	l, baseURL := listenerBaseURL(t)
	tornDown := make(chan bool, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.WriteMessage(conn, websockettest.OpText, []byte("TERMINAL"))

		// The stream's own Close() (via readLoop's teardown defer) sends a
		// close frame before tearing down the socket; drain it first so the
		// raw Read below can't spuriously observe those buffered bytes
		// instead of the eventual EOF/reset.
		websockettest.ReadMessage(conn)

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, rerr := conn.Read(b[:])
		var netErr net.Error
		isTimeout := errors.As(rerr, &netErr) && netErr.Timeout()
		tornDown <- rerr != nil && !isTimeout
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	// Deliberately not calling s.Close(): readLoop's own exit path must be
	// the thing that closes the conn.

	for range s.Events() {
	}

	select {
	case ok := <-tornDown:
		if !ok {
			t.Error("expected the client to have closed its socket after the terminal message without Close() being called")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting to observe the client's socket closure")
	}
}

func TestCtxCancelMidStream(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.ReadMessage(conn) // block until client disconnects
	}()

	conn := dialClient(t, baseURL)
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config[testEvent]{Ctx: ctx, Conn: conn, Decode: echoDecode})
	defer s.Close()

	cancel()

	for range s.Events() {
	}
	if !errors.Is(s.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want context.Canceled", s.Err())
	}
}

func TestServerCloseIsCleanEnd(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		websockettest.WriteClose(conn, 1000, "done")
	}()

	conn := dialClient(t, baseURL)
	s := New(Config[testEvent]{Ctx: context.Background(), Conn: conn, Decode: echoDecode})
	defer s.Close()

	for range s.Events() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestDialURL(t *testing.T) {
	tests := []struct {
		base, path, want string
		wantErr          bool
	}{
		{"http://example.com", "/v1/listen", "ws://example.com/v1/listen", false},
		{"https://example.com", "/v1/listen", "wss://example.com/v1/listen", false},
		{"https://example.com/", "/v1/listen", "wss://example.com/v1/listen", false},
		{"https://example.com/base/", "/v1/listen", "wss://example.com/base/v1/listen", false},
		{"ftp://example.com", "/v1/listen", "", true},
		{"://bad-url", "/v1/listen", "", true},
	}
	for _, tt := range tests {
		u, err := DialURL(tt.base, tt.path)
		if tt.wantErr {
			if err == nil {
				t.Errorf("DialURL(%q, %q): want error, got nil", tt.base, tt.path)
			}
			continue
		}
		if err != nil {
			t.Errorf("DialURL(%q, %q): %v", tt.base, tt.path, err)
			continue
		}
		if u.String() != tt.want {
			t.Errorf("DialURL(%q, %q) = %q, want %q", tt.base, tt.path, u.String(), tt.want)
		}
	}
}
