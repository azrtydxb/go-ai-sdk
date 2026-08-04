package openai

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket/websockettest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func listenerBaseURL(t *testing.T) (net.Listener, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l, "http://" + l.Addr().String()
}

// requestCapturingUpgrade performs the server-side WebSocket handshake like
// websockettest.Upgrade, but also captures the client's Authorization /
// OpenAI-Beta headers and full request URL for assertions.
func requestCapturingUpgrade(conn net.Conn, gotAuth, gotBeta, gotURL *string) *bufio.Reader {
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return br
	}
	defer req.Body.Close()
	*gotAuth = req.Header.Get("Authorization")
	*gotBeta = req.Header.Get("OpenAI-Beta")
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

func TestStreamTranscribe_HandshakeAndSessionUpdate(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	var gotAuth, gotBeta, gotURL string
	var gotSessionMsg []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		requestCapturingUpgrade(conn, &gotAuth, &gotBeta, &gotURL)
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			gotSessionMsg = payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("oa-key"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{
		MediaType: "audio/pcm",
		Language:  "en",
		ProviderOptions: map[string]any{
			"openai": map[string]any{"turn_detection": nil},
		},
	})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	<-done

	if gotAuth != "Bearer oa-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer oa-key")
	}
	if gotBeta != "realtime=v1" {
		t.Fatalf("OpenAI-Beta = %q, want %q", gotBeta, "realtime=v1")
	}
	q := mustParseQuery(t, gotURL)
	if q.Get("intent") != "transcription" {
		t.Fatalf("intent query param missing/wrong: %q", gotURL)
	}

	var msg map[string]any
	if err := json.Unmarshal(gotSessionMsg, &msg); err != nil {
		t.Fatalf("decode session update: %v (%s)", err, gotSessionMsg)
	}
	if msg["type"] != "transcription_session.update" {
		t.Fatalf("type = %v, want transcription_session.update", msg["type"])
	}
	session, ok := msg["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing or wrong type: %+v", msg)
	}
	if session["input_audio_format"] != "pcm16" {
		t.Fatalf("input_audio_format = %v, want pcm16", session["input_audio_format"])
	}
	iat, ok := session["input_audio_transcription"].(map[string]any)
	if !ok {
		t.Fatalf("input_audio_transcription missing: %+v", session)
	}
	if iat["model"] != "gpt-4o-transcribe" || iat["language"] != "en" {
		t.Fatalf("input_audio_transcription = %+v", iat)
	}
	if _, ok := session["turn_detection"]; !ok {
		t.Fatalf("ProviderOptions[\"openai\"] not merged into session: %+v", session)
	}
}

func TestStreamTranscribe_SendBase64Passthrough(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	msgCh := make(chan []byte, 8)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		// Session update is the first client message; drain it.
		websockettest.ReadMessage(conn)
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			msgCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	if err := stream.Send(context.Background(), []byte("pcm-bytes")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-msgCh:
		var msg map[string]any
		if err := json.Unmarshal(got, &msg); err != nil {
			t.Fatalf("decode append message: %v", err)
		}
		if msg["type"] != "input_audio_buffer.append" {
			t.Fatalf("type = %v, want input_audio_buffer.append", msg["type"])
		}
		wantB64 := base64.StdEncoding.EncodeToString([]byte("pcm-bytes"))
		if msg["audio"] != wantB64 {
			t.Fatalf("audio = %v, want %v", msg["audio"], wantB64)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for append message")
	}

	for range stream.Events() {
	}
}

func TestStreamTranscribe_EventSequenceDeltaThenCompleted(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update

		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"conversation.item.input_audio_transcription.delta","delta":"hel"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"conversation.item.input_audio_transcription.completed","transcript":"hello"}`))
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
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
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Text != "hel" || got[0].Final {
		t.Fatalf("event 0 = %+v", got[0])
	}
	if got[1].Text != "hello" || !got[1].Final {
		t.Fatalf("event 1 = %+v", got[1])
	}
}

func TestStreamTranscribe_ErrorEventSetsErr(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"error","error":{"message":"invalid audio format"}}`))
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	for range stream.Events() {
	}
	if stream.Err() == nil {
		t.Fatal("Err() = nil, want the error event's message")
	}
}

// --- Fix wave MINOR 10 — readLoop must close the underlying conn on a
// clean non-socket termination (an "error" event), not just leave the TCP
// connection lingering until the caller eventually calls Close(). ---

func TestStreamTranscribe_ErrorEventClosesUnderlyingConn(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"error","error":{"message":"invalid audio format"}}`))

		// The stream's own Close() (called by the fixed readLoop) sends a
		// close frame before tearing down the socket; drain it first so
		// the raw Read below can't spuriously observe those buffered
		// bytes instead of the eventual EOF/reset.
		websockettest.ReadMessage(conn)

		// Without calling stream.Close(), confirm the client tore down its
		// socket as soon as readLoop saw the terminal "error" event,
		// rather than leaving the TCP connection open until some later
		// Close().
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, rerr := conn.Read(b[:])
		var netErr net.Error
		isTimeout := errors.As(rerr, &netErr) && netErr.Timeout()
		tornDown <- rerr != nil && !isTimeout
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	// Deliberately not calling stream.Close(): readLoop's own exit path
	// must be the thing that closes the conn.

	for range stream.Events() {
	}

	select {
	case ok := <-tornDown:
		if !ok {
			t.Error("expected the client to have closed its socket after the terminal error event without stream.Close() being called")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting to observe the client's socket closure")
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
		websockettest.ReadMessage(conn) // session update
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
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	defer stream.Close()

	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}
	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend (2nd): %v", err)
	}

	select {
	case f := <-frameCh:
		if f.opcode != websockettest.OpText || string(f.payload) != `{"type":"input_audio_buffer.commit"}` {
			t.Fatalf("frame = opcode=%d payload=%q", f.opcode, f.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for commit frame")
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
		websockettest.ReadMessage(conn) // session update
		websockettest.WriteClose(conn, 1000, "done")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
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

func TestStreamTranscribe_CtxCancelMidStream(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		websockettest.ReadMessage(conn) // block until client disconnects
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
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
		websockettest.ReadMessage(conn)
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
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

// TestStreamTranscribe_AbandonedEventsThenCloseUnblocksReadLoop pins the fix
// for a reader-goroutine leak: readLoop's per-event send used to select
// only on the events channel and ctx.Done(), so a consumer that stops
// ranging over Events() (the documented "break, then Close()" cleanup
// pattern) without cancelling ctx left readLoop parked forever on that send
// once the internal event-channel buffer filled up. Close() must unblock
// it.
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
		websockettest.ReadMessage(conn) // session update
		// Flood far more delta events than the stream's internal
		// event-channel buffer (32) so the reader goroutine is guaranteed
		// to still be blocked trying to deliver one when the consumer
		// below abandons Events().
		for i := 0; i < 200; i++ {
			websockettest.WriteMessage(conn, websockettest.OpText, []byte(
				`{"type":"conversation.item.input_audio_transcription.delta","delta":"x"}`))
		}
		websockettest.ReadMessage(conn) // block until client disconnects
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	streamIface, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	stream := streamIface.(*realtimeStream)

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
		websockettest.ReadMessage(conn) // session update
		websockettest.ReadMessage(conn) // commit
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
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

// TestStreamTranscribe_SendAfterCloseErrorMessage pins the exact,
// provider-specific error message a Send after Close must return: the
// wsstream migration must not let wsstream's own "wsstream: send called
// after close" leak through in place of "openai: Send called after
// Close".
func TestStreamTranscribe_SendAfterCloseErrorMessage(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.Upgrade(conn)
		websockettest.ReadMessage(conn) // session update
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	err = stream.Send(context.Background(), []byte("late"))
	if err == nil {
		t.Fatal("Send after Close: want error, got nil")
	}
	if got, want := err.Error(), "openai: Send called after Close"; got != want {
		t.Fatalf("Send after Close error = %q, want %q", got, want)
	}

	for range stream.Events() {
	}
}

// TestStreamTranscribe_CloseSendAfterCloseNoWsstreamLeak pins that
// CloseSend after Close never surfaces wsstream's own raw error string in
// place of whatever this provider documents.
func TestStreamTranscribe_CloseSendAfterCloseNoWsstreamLeak(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		websockettest.Upgrade(conn)
		websockettest.ReadMessage(conn) // session update
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	m := p.StreamingTranscriptionModel("gpt-4o-transcribe")
	stream, err := m.StreamTranscribe(context.Background(), provider.StreamTranscriptionCall{})
	if err != nil {
		t.Fatalf("StreamTranscribe: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := stream.CloseSend(context.Background()); err != nil {
		t.Fatalf("CloseSend after Close: want nil, got %v", err)
	}

	for range stream.Events() {
	}
}
