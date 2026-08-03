package deepgram

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket/websockettest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// requestCapturingUpgrade performs the server-side WebSocket handshake like
// websockettest.Upgrade, but also captures the client's Authorization
// header and full request URL (scheme-relative, since http.ReadRequest
// only sees the request line) for assertions.
func requestCapturingUpgrade(conn net.Conn, gotAuth, gotURL *string) *bufio.Reader {
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return br
	}
	defer req.Body.Close()
	*gotAuth = req.Header.Get("Authorization")
	*gotURL = req.URL.String()

	key := req.Header.Get("Sec-WebSocket-Key")
	accept := websockettest.ComputeAccept(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	conn.Write([]byte(resp))
	return br
}

func mustParseQuery(t *testing.T, rawURL string) url.Values {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", rawURL, err)
	}
	return u.Query()
}

// listenerBaseURL starts a TCP listener and returns it plus the "http://"
// base URL fixture servers derive their ws:// dial URL from (via the
// scheme-swap rule StreamTranscribe implements).
func listenerBaseURL(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l, "http://" + l.Addr().String()
}

func TestStreamTranscribe_HandshakeAndQuery(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	var gotAuth string
	var gotURL string
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := requestCapturingUpgrade(conn, &gotAuth, &gotURL)
		_ = br
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{"type":"Metadata"}`))
	}()

	p := New(WithAPIKey("dg-key"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{
		MediaType:  "audio/pcm;rate=16000",
		Language:   "en",
		SampleRate: 16000,
		ProviderOptions: map[string]any{
			"deepgram": map[string]any{"punctuate": true},
		},
	})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	<-done

	if gotAuth != "Token dg-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Token dg-key")
	}
	q := mustParseQuery(t, gotURL)
	if q.Get("model") != "nova-3" || q.Get("language") != "en" {
		t.Fatalf("query = %q", gotURL)
	}
	if q.Get("encoding") != "linear16" || q.Get("sample_rate") != "16000" {
		t.Fatalf("expected linear16/16000 from audio/pcm;rate=16000, got query = %q", gotURL)
	}
	if q.Get("punctuate") != "true" {
		t.Fatalf("expected ProviderOptions merged into query, got %q", gotURL)
	}
}

func TestStreamTranscribe_BinaryAudioPassthrough(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	audioCh := make(chan []byte, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		opcode, payload, err := websockettest.ReadMessage(conn)
		if err == nil && opcode == websockettest.OpBinary {
			audioCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	if err := stream.Send(context.Background(), []byte("raw-pcm-bytes")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-audioCh:
		if string(got) != "raw-pcm-bytes" {
			t.Fatalf("server received %q, want %q", got, "raw-pcm-bytes")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to receive audio")
	}

	for range stream.Events() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (clean server close)", err)
	}
}

func TestStreamTranscribe_EventSequenceInterimThenFinal(t *testing.T) {
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
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{
			"type":"Results","is_final":false,"start":0.0,"duration":0.5,
			"channel":{"alternatives":[{"transcript":"hel"}]}
		}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{
			"type":"Results","is_final":true,"start":0.0,"duration":1.0,
			"channel":{"alternatives":[{"transcript":"hello"}]}
		}`))
		// An empty transcript must be skipped (no event emitted).
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{
			"type":"Results","is_final":false,"start":1.0,"duration":0.1,
			"channel":{"alternatives":[{"transcript":""}]}
		}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{"type":"Metadata"}`))
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	var got []provider.TranscriptEvent
	for e := range stream.Events() {
		got = append(got, e)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (Metadata = clean end)", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Text != "hel" || got[0].Final {
		t.Fatalf("event 0 = %+v, want interim %q", got[0], "hel")
	}
	if got[1].Text != "hello" || !got[1].Final || got[1].StartSec != 0.0 || got[1].EndSec != 1.0 {
		t.Fatalf("event 1 = %+v, want final %q [0,1]", got[1], "hello")
	}
}

func TestStreamTranscribe_CloseSendWireShape(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	frameCh := make(chan struct {
		opcode  int
		payload []byte
	}, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		opcode, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			frameCh <- struct {
				opcode  int
				payload []byte
			}{opcode, payload}
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	// Idempotent: a second call must not write a second frame.
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend (2nd): %v", err)
	}

	select {
	case f := <-frameCh:
		if f.opcode != websockettest.OpText || string(f.payload) != `{"type":"CloseStream"}` {
			t.Fatalf("frame = opcode=%d payload=%q, want text {\"type\":\"CloseStream\"}", f.opcode, f.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for CloseStream frame")
	}

	for range stream.Events() {
	}
}

func TestStreamTranscribe_ServerCloseIsCleanEnd(t *testing.T) {
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

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestStreamTranscribe_MalformedMessageIsError(t *testing.T) {
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
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`not json`))
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	if stream.Err() == nil {
		t.Fatal("Err() = nil, want a decode error")
	}
}

func TestStreamTranscribe_CtxCancelMidStream(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		// Never send anything; block until the client disconnects.
		websockettest.ReadMessage(conn)
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := m.StreamTranscribe(ctx, provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	cancel()

	for range stream.Events() {
	}
	if !errors.Is(stream.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want context.Canceled", stream.Err())
	}
}

func TestStreamTranscribe_CloseIdempotent(t *testing.T) {
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

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close (2nd): %v", err)
	}

	for range stream.Events() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil after explicit Close", err)
	}
}

// TestStreamTranscribe_ConcurrentSendWhileRangingEvents exercises the
// documented concurrency contract (one goroutine may Send audio while
// another ranges over Events) against a real fixture connection, under
// -race.
func TestStreamTranscribe_ConcurrentSendWhileRangingEvents(t *testing.T) {
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
		for i := 0; i < 20; i++ {
			if _, _, err := websockettest.ReadMessage(conn); err != nil {
				return
			}
			websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{
				"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"x"}]}
			}`))
		}
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{"type":"Metadata"}`))
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		for i := 0; i < 20; i++ {
			_ = stream.Send(context.Background(), []byte{byte(i)})
		}
	}()

	count := 0
	for range stream.Events() {
		count++
	}
	<-sendDone
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if count != 20 {
		t.Fatalf("count = %d, want 20", count)
	}
}

// TestStreamTranscribe_AbandonedEventsThenCloseUnblocksReadLoop pins the fix
// for a reader-goroutine leak: readLoop's per-event send used to select
// only on the events channel and ctx.Done(), so a consumer that stops
// ranging over Events() (the documented "break, then Close()" cleanup
// pattern) without cancelling ctx left readLoop parked forever on that send
// once the fixture-server-fed buffer filled up. Close() must unblock it.
func TestStreamTranscribe_AbandonedEventsThenCloseUnblocksReadLoop(t *testing.T) {
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
		// Flood far more Results messages than the stream's internal
		// event-channel buffer (32) so the reader goroutine is guaranteed
		// to still be blocked trying to deliver one when the consumer
		// below abandons Events().
		for i := 0; i < 200; i++ {
			websockettest.WriteMessage(conn, websockettest.OpText, []byte(`{
				"type":"Results","is_final":false,"channel":{"alternatives":[{"transcript":"x"}]}
			}`))
		}
		// Keep the connection open; the client side closes it via Close().
		websockettest.ReadMessage(conn)
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	streamIface, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	stream := streamIface.(*liveStream)

	// Consume a handful of events, then abandon Events() by breaking out
	// of the range — the documented cleanup path is to call Close(),
	// without ever cancelling a ctx.
	n := 0
	for range stream.Events() {
		n++
		if n == 3 {
			break
		}
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-stream.readLoopDone:
		// reader goroutine exited, as required.
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop leaked: did not exit within 2s of Close() after Events() was abandoned")
	}
}

func TestStreamTranscribe_SendAfterCloseSend(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.Upgrade(conn)
		websockettest.ReadMessage(conn) // CloseStream
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("nova-3")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	if err := stream.Send(context.Background(), []byte("late")); err == nil {
		t.Fatal("Send after CloseSend: want error, got nil")
	}

	for range stream.Events() {
	}
}
