package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket/websockettest"
)

// listenerBaseURL, requestCapturingUpgrade, and mustParseQuery are defined
// in realtime_transcription_test.go and reused here.

func TestRealtimeSession_HandshakeAndSessionUpdate(t *testing.T) {
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
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{
		Model:             "gpt-4o-realtime-preview",
		Voice:             "alloy",
		Instructions:      "be terse",
		InputAudioFormat:  "pcm16",
		OutputAudioFormat: "pcm16",
		ProviderOptions: map[string]any{
			"openai": map[string]any{"turn_detection": nil},
		},
	})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	for range session.Events() {
	}
	<-done

	if gotAuth != "Bearer oa-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer oa-key")
	}
	if gotBeta != "realtime=v1" {
		t.Fatalf("OpenAI-Beta = %q, want %q", gotBeta, "realtime=v1")
	}
	q := mustParseQuery(t, gotURL)
	if q.Get("model") != "gpt-4o-realtime-preview" {
		t.Fatalf("model query param = %q, want gpt-4o-realtime-preview", q.Get("model"))
	}

	var msg map[string]any
	if err := json.Unmarshal(gotSessionMsg, &msg); err != nil {
		t.Fatalf("decode session update: %v (%s)", err, gotSessionMsg)
	}
	if msg["type"] != "session.update" {
		t.Fatalf("type = %v, want session.update", msg["type"])
	}
	session_, ok := msg["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing or wrong type: %+v", msg)
	}
	if session_["voice"] != "alloy" {
		t.Fatalf("voice = %v, want alloy", session_["voice"])
	}
	if session_["instructions"] != "be terse" {
		t.Fatalf("instructions = %v, want 'be terse'", session_["instructions"])
	}
	if session_["input_audio_format"] != "pcm16" {
		t.Fatalf("input_audio_format = %v, want pcm16", session_["input_audio_format"])
	}
	if session_["output_audio_format"] != "pcm16" {
		t.Fatalf("output_audio_format = %v, want pcm16", session_["output_audio_format"])
	}
	if _, ok := session_["turn_detection"]; !ok {
		t.Fatalf("ProviderOptions[\"openai\"] not merged into session: %+v", session_)
	}
}

func TestRealtimeSession_SessionUpdateOmitsEmptyFields(t *testing.T) {
	l, baseURL := listenerBaseURL(t)

	var gotSessionMsg []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if err := websockettest.Upgrade(conn); err != nil {
			return
		}
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			gotSessionMsg = payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "gpt-4o-realtime-preview"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	for range session.Events() {
	}
	<-done

	var msg map[string]any
	if err := json.Unmarshal(gotSessionMsg, &msg); err != nil {
		t.Fatalf("decode session update: %v (%s)", err, gotSessionMsg)
	}
	sessionObj, ok := msg["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing or wrong type: %+v", msg)
	}
	if len(sessionObj) != 0 {
		t.Fatalf("session = %+v, want empty (all fields omitted)", sessionObj)
	}
}

func TestRealtimeSession_SendAudioWireShape(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			msgCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	if err := session.SendAudio(context.Background(), []byte("pcm-bytes")); err != nil {
		t.Fatalf("SendAudio: %v", err)
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

	for range session.Events() {
	}
}

func TestRealtimeSession_CommitAudioWireShape(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			msgCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	if err := session.CommitAudio(context.Background()); err != nil {
		t.Fatalf("CommitAudio: %v", err)
	}

	select {
	case got := <-msgCh:
		if string(got) != `{"type":"input_audio_buffer.commit"}` {
			t.Fatalf("payload = %q, want input_audio_buffer.commit", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for commit message")
	}

	for range session.Events() {
	}
}

func TestRealtimeSession_SendTextWireShape(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			msgCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	if err := session.SendText(context.Background(), "hello there"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	select {
	case got := <-msgCh:
		var msg map[string]any
		if err := json.Unmarshal(got, &msg); err != nil {
			t.Fatalf("decode item.create message: %v", err)
		}
		if msg["type"] != "conversation.item.create" {
			t.Fatalf("type = %v, want conversation.item.create", msg["type"])
		}
		item, ok := msg["item"].(map[string]any)
		if !ok {
			t.Fatalf("item missing or wrong type: %+v", msg)
		}
		if item["type"] != "message" || item["role"] != "user" {
			t.Fatalf("item = %+v, want type=message role=user", item)
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("content = %+v, want one part", item["content"])
		}
		part, ok := content[0].(map[string]any)
		if !ok || part["type"] != "input_text" || part["text"] != "hello there" {
			t.Fatalf("content[0] = %+v, want input_text 'hello there'", content[0])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for item.create message")
	}

	for range session.Events() {
	}
}

func TestRealtimeSession_CreateResponseWireShape(t *testing.T) {
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
		websockettest.ReadMessage(conn) // session update
		_, payload, err := websockettest.ReadMessage(conn)
		if err == nil {
			msgCh <- payload
		}
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	if err := session.CreateResponse(context.Background()); err != nil {
		t.Fatalf("CreateResponse: %v", err)
	}

	select {
	case got := <-msgCh:
		if string(got) != `{"type":"response.create"}` {
			t.Fatalf("payload = %q, want response.create", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response.create message")
	}

	for range session.Events() {
	}
}

func TestRealtimeSession_EventSurfacing(t *testing.T) {
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

		audioB64 := base64.StdEncoding.EncodeToString([]byte("pcm-out"))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.output_audio.delta","delta":"`+audioB64+`"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.audio.delta","delta":"`+audioB64+`"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.output_text.delta","delta":"hel"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.text.delta","delta":"lo"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.audio_transcript.delta","delta":"transcript"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"session.created"}`))
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	var got []RealtimeEvent
	for e := range session.Events() {
		got = append(got, e)
	}
	if err := session.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(got) != 6 {
		t.Fatalf("got %d events, want 6: %+v", len(got), got)
	}
	if got[0].Type != "response.output_audio.delta" || string(got[0].AudioDelta) != "pcm-out" {
		t.Fatalf("event 0 = %+v", got[0])
	}
	if len(got[0].Raw) == 0 {
		t.Fatal("event 0 Raw should be set")
	}
	if got[1].Type != "response.audio.delta" || string(got[1].AudioDelta) != "pcm-out" {
		t.Fatalf("event 1 = %+v", got[1])
	}
	if got[2].Type != "response.output_text.delta" || got[2].TextDelta != "hel" {
		t.Fatalf("event 2 = %+v", got[2])
	}
	if got[3].Type != "response.text.delta" || got[3].TextDelta != "lo" {
		t.Fatalf("event 3 = %+v", got[3])
	}
	if got[4].Type != "response.audio_transcript.delta" || got[4].TextDelta != "transcript" {
		t.Fatalf("event 4 = %+v", got[4])
	}
	if got[5].Type != "session.created" || got[5].AudioDelta != nil || got[5].TextDelta != "" {
		t.Fatalf("event 5 (unknown type) = %+v, want Raw-only", got[5])
	}
	if len(got[5].Raw) == 0 {
		t.Fatal("event 5 Raw should be set even for unknown type")
	}
}

// TestRealtimeSession_CorruptAudioDeltaDeliversRawOnly pins Task 6's fix 3:
// a base64-corrupt "delta" field inside an otherwise well-formed audio-delta
// event must not fail the whole event (or the session) — the event is still
// delivered with Raw set, AudioDelta left nil, and iteration/subsequent
// events unaffected.
func TestRealtimeSession_CorruptAudioDeltaDeliversRawOnly(t *testing.T) {
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
			`{"type":"response.output_audio.delta","delta":"not-valid-base64!!"}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.output_text.delta","delta":"after"}`))
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	var got []RealtimeEvent
	for e := range session.Events() {
		got = append(got, e)
	}
	if err := session.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (a corrupt delta must not fail the session)", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}

	corrupt := got[0]
	if corrupt.Type != "response.output_audio.delta" {
		t.Fatalf("event 0 Type = %q, want response.output_audio.delta", corrupt.Type)
	}
	if corrupt.AudioDelta != nil {
		t.Fatalf("event 0 AudioDelta = %v, want nil for an undecodable delta", corrupt.AudioDelta)
	}
	if len(corrupt.Raw) == 0 {
		t.Fatal("event 0 Raw should still be set for a corrupt delta")
	}
	if !strings.Contains(string(corrupt.Raw), "not-valid-base64!!") {
		t.Fatalf("event 0 Raw = %s, want it to contain the undecoded delta", corrupt.Raw)
	}

	// The stream must keep going after the corrupt delta.
	if got[1].Type != "response.output_text.delta" || got[1].TextDelta != "after" {
		t.Fatalf("event 1 = %+v", got[1])
	}
}

func TestRealtimeSession_ErrorEventRecordedIterationContinues(t *testing.T) {
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
			`{"type":"error","error":{"message":"bad request"}}`))
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(
			`{"type":"response.output_text.delta","delta":"still here"}`))
		websockettest.WriteClose(conn, 1000, "")
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	var got []RealtimeEvent
	for e := range session.Events() {
		got = append(got, e)
	}
	// The error event must not end iteration, and must not set Err() —
	// only a socket failure does that.
	if err := session.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (error event must not set it)", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2 (error event + subsequent delta): %+v", len(got), got)
	}
	if got[0].Type != "error" {
		t.Fatalf("event 0 type = %q, want error", got[0].Type)
	}
	if got[1].Type != "response.output_text.delta" || got[1].TextDelta != "still here" {
		t.Fatalf("event 1 = %+v", got[1])
	}
}

func TestRealtimeSession_ServerCloseIsCleanEnd(t *testing.T) {
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
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	for range session.Events() {
	}
	if err := session.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

// --- Task 6 — readLoop must close the underlying conn on a
// readLoop-terminating decode error, not just leave the TCP connection
// lingering until the caller eventually calls Close(). Mirrors
// TestStreamTranscribe_ErrorEventClosesUnderlyingConn in
// realtime_transcription_test.go and
// TestStreamTranscribe_MetadataEndClosesUnderlyingConn in
// providers/deepgram/live_test.go, which already pin the same invariant for
// this session type's sibling readLoops. ---

func TestRealtimeSession_MalformedMessageClosesUnderlyingConn(t *testing.T) {
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
		websockettest.WriteMessage(conn, websockettest.OpText, []byte(`not json`))

		// The session's own Close() (called by the fixed readLoop) sends a
		// close frame before tearing down the socket; drain it first so the
		// raw Read below can't spuriously observe those buffered bytes
		// instead of the eventual EOF/reset.
		websockettest.ReadMessage(conn)

		// Without calling session.Close(), confirm the client tore down its
		// socket as soon as readLoop saw the decode error, rather than
		// leaving the TCP connection open until some later Close().
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, rerr := conn.Read(b[:])
		var netErr net.Error
		isTimeout := errors.As(rerr, &netErr) && netErr.Timeout()
		tornDown <- rerr != nil && !isTimeout
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	// Deliberately not calling session.Close(): readLoop's own exit path
	// must be the thing that closes the conn.

	for range session.Events() {
	}
	if session.Err() == nil {
		t.Fatal("Err() = nil, want a decode error")
	}

	select {
	case ok := <-tornDown:
		if !ok {
			t.Error("expected the client to have closed its socket after the decode error without session.Close() being called")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting to observe the client's socket closure")
	}
}

func TestRealtimeSession_CtxCancelMidSession(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	session, err := p.RealtimeSession(ctx, RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	defer session.Close()

	cancel()

	for range session.Events() {
	}
	if !errors.Is(session.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want context.Canceled", session.Err())
	}
}

func TestRealtimeSession_CloseIdempotent(t *testing.T) {
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
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close (2nd): %v", err)
	}

	for range session.Events() {
	}
	if err := session.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil after explicit Close", err)
	}
}

func TestRealtimeSession_SendAfterCloseReturnsError(t *testing.T) {
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
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := session.SendAudio(context.Background(), []byte("x")); err == nil {
		t.Fatal("SendAudio after Close: want error, got nil")
	} else if got, want := err.Error(), "openai: SendAudio called after Close"; got != want {
		t.Errorf("SendAudio after Close error = %q, want %q", got, want)
	}
	if err := session.CommitAudio(context.Background()); err == nil {
		t.Fatal("CommitAudio after Close: want error, got nil")
	} else if got, want := err.Error(), "openai: CommitAudio called after Close"; got != want {
		t.Errorf("CommitAudio after Close error = %q, want %q", got, want)
	}
	if err := session.SendText(context.Background(), "x"); err == nil {
		t.Fatal("SendText after Close: want error, got nil")
	} else if got, want := err.Error(), "openai: SendText called after Close"; got != want {
		t.Errorf("SendText after Close error = %q, want %q", got, want)
	}
	if err := session.CreateResponse(context.Background()); err == nil {
		t.Fatal("CreateResponse after Close: want error, got nil")
	} else if got, want := err.Error(), "openai: CreateResponse called after Close"; got != want {
		t.Errorf("CreateResponse after Close error = %q, want %q", got, want)
	}

	for range session.Events() {
	}
}

// TestRealtimeSession_AbandonedEventsThenCloseUnblocksReadLoop pins the fix
// for a reader-goroutine leak (same shape as the Task 3 fix): readLoop's
// per-event send must not block forever when a consumer stops ranging
// over Events() without cancelling ctx — Close() must unblock it.
func TestRealtimeSession_AbandonedEventsThenCloseUnblocksReadLoop(t *testing.T) {
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
		// Flood far more delta events than the session's internal
		// event-channel buffer (32) so the reader goroutine is guaranteed
		// to still be blocked trying to deliver one when the consumer
		// below abandons Events().
		for i := 0; i < 200; i++ {
			websockettest.WriteMessage(conn, websockettest.OpText, []byte(
				`{"type":"response.output_text.delta","delta":"x"}`))
		}
		websockettest.ReadMessage(conn) // block until client disconnects
	}()

	p := New(WithAPIKey("k"), WithBaseURL(baseURL))
	session, err := p.RealtimeSession(context.Background(), RealtimeConfig{Model: "m"})
	if err != nil {
		t.Fatalf("RealtimeSession: %v", err)
	}

	// Consume a handful of events, then abandon Events() by breaking out
	// of the range — the documented cleanup path is to call Close(),
	// without ever cancelling a ctx.
	n := 0
	for range session.Events() {
		n++
		if n == 3 {
			break
		}
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-session.readLoopDone:
		// reader goroutine exited, as required.
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop leaked: did not exit within 2s of Close() after Events() was abandoned")
	}
}
