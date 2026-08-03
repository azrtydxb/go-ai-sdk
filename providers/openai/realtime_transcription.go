package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// streamingTranscriptionModel implements provider.StreamingTranscriptionModel
// against OpenAI's Realtime API in transcription-only mode.
type streamingTranscriptionModel struct {
	provider *Provider
	modelID  string
}

// StreamingTranscriptionModel returns a provider.StreamingTranscriptionModel
// for the given OpenAI transcription model ID (e.g. "gpt-4o-transcribe").
func (p *Provider) StreamingTranscriptionModel(id string) provider.StreamingTranscriptionModel {
	return &streamingTranscriptionModel{provider: p, modelID: id}
}

func (m *streamingTranscriptionModel) ModelID() string      { return m.modelID }
func (m *streamingTranscriptionModel) ProviderName() string { return "openai" }

// StreamTranscribe dials OpenAI's Realtime WebSocket endpoint in
// transcription mode, sends the initial transcription_session.update, and
// returns an open provider.TranscriptionStream.
//
// The dial URL is derived from the provider's configured baseURL by
// swapping http(s) for ws(s) — never hardcoded — so fixture servers set up
// via WithBaseURL work the same as the real OpenAI host.
func (m *streamingTranscriptionModel) StreamTranscribe(ctx context.Context, call provider.StreamTranscriptionCall) (provider.TranscriptionStream, error) {
	dialURL, err := realtimeDialURL(m.provider.baseURL)
	if err != nil {
		return nil, err
	}

	conn, err := websocket.Dial(ctx, dialURL, websocket.DialOptions{
		Header: http.Header{
			"Authorization": []string{"Bearer " + m.provider.apiKey},
			"OpenAI-Beta":   []string{"realtime=v1"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: dial realtime transcription: %w", err)
	}

	sessionUpdate, err := buildSessionUpdate(m.modelID, call)
	if err != nil {
		conn.Close(websocket.CloseNormal, "")
		return nil, err
	}
	if err := conn.WriteText(ctx, sessionUpdate); err != nil {
		conn.Close(websocket.CloseNormal, "")
		return nil, fmt.Errorf("openai: send transcription_session.update: %w", err)
	}

	s := &realtimeStream{
		ctx:          ctx,
		conn:         conn,
		events:       make(chan provider.TranscriptEvent, 32),
		closeCh:      make(chan struct{}),
		readLoopDone: make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// realtimeDialURL derives the wss:// (or ws://, for test fixtures) URL for
// OpenAI's Realtime endpoint in transcription-intent mode from baseURL.
func realtimeDialURL(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("openai: parse base URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("openai: unsupported base URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	q := url.Values{}
	q.Set("intent", "transcription")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildSessionUpdate builds the transcription_session.update message sent
// as soon as the socket is open.
func buildSessionUpdate(modelID string, call provider.StreamTranscriptionCall) ([]byte, error) {
	inputAudioTranscription := map[string]any{"model": modelID}
	if call.Language != "" {
		inputAudioTranscription["language"] = call.Language
	}

	session := map[string]any{
		"input_audio_format":        audioFormatFor(call.MediaType),
		"input_audio_transcription": inputAudioTranscription,
	}
	if opts, ok := call.ProviderOptions["openai"].(map[string]any); ok {
		for k, v := range opts {
			session[k] = v
		}
	}

	msg := map[string]any{
		"type":    "transcription_session.update",
		"session": session,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("openai: encode transcription_session.update: %w", err)
	}
	return data, nil
}

// audioFormatFor maps a MediaType to OpenAI's input_audio_format: "pcm16"
// for "audio/pcm" (params such as ";rate=..." are ignored — OpenAI's
// Realtime API always expects 24kHz mono PCM16 regardless), and "pcm16" as
// the default for anything else (including an empty MediaType).
func audioFormatFor(mediaType string) string {
	base := mediaType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		base = mediaType[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	switch base {
	case "audio/pcm", "":
		return "pcm16"
	default:
		return "pcm16"
	}
}

// realtimeStream implements provider.TranscriptionStream against an open
// OpenAI Realtime WebSocket connection in transcription mode.
type realtimeStream struct {
	ctx    context.Context // governs the reader goroutine's Read calls
	conn   *websocket.Conn
	events chan provider.TranscriptEvent

	// closeCh is closed exactly once, by Close(), to unblock readLoop if
	// it's parked trying to send a buffered event to a consumer that has
	// already stopped ranging over Events() without cancelling ctx.
	closeCh chan struct{}
	// readLoopDone is closed when readLoop returns, after events is
	// closed. Not part of the public interface; exists so tests (and
	// Close, defensively) can observe that the reader goroutine has
	// actually exited rather than just that events was closed.
	readLoopDone chan struct{}

	writeMu       sync.Mutex // serializes Send/CloseSend/Close (conn write contract)
	closed        bool
	closeSendSent bool

	errMu sync.Mutex
	err   error
}

// Send implements provider.TranscriptionStream by base64-encoding audio
// into an input_audio_buffer.append event.
func (s *realtimeStream) Send(ctx context.Context, audio []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("openai: Send called after Close")
	}
	if s.closeSendSent {
		return errors.New("openai: Send called after CloseSend")
	}
	msg, err := json.Marshal(map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
	if err != nil {
		return fmt.Errorf("openai: encode input_audio_buffer.append: %w", err)
	}
	return s.conn.WriteText(ctx, msg)
}

// CloseSend implements provider.TranscriptionStream by sending
// input_audio_buffer.commit. Idempotent.
func (s *realtimeStream) CloseSend(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed || s.closeSendSent {
		return nil
	}
	s.closeSendSent = true
	return s.conn.WriteText(ctx, []byte(`{"type":"input_audio_buffer.commit"}`))
}

// Close implements provider.TranscriptionStream by aborting the connection
// without flushing, without waiting for outstanding events to be consumed.
// Idempotent. Because abort is caller-initiated rather than a stream
// failure, Err() reports nil once the stream ends as a result of Close
// (matching a peer-initiated clean close) rather than surfacing whatever
// error the now-closed connection produces on its next Read.
func (s *realtimeStream) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.conn.Close(websocket.CloseNormal, "")
}

func (s *realtimeStream) isClosed() bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.closed
}

// Events implements provider.TranscriptionStream. Single use: it ranges
// over the channel the reader goroutine populates, which is closed exactly
// once when the stream ends.
func (s *realtimeStream) Events() iter.Seq[provider.TranscriptEvent] {
	return func(yield func(provider.TranscriptEvent) bool) {
		for e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

// Err implements provider.TranscriptionStream.
func (s *realtimeStream) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *realtimeStream) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

// readLoop pumps conn.Read into s.events until the stream ends, then closes
// s.events. It runs in its own goroutine, started by StreamTranscribe.
func (s *realtimeStream) readLoop() {
	defer close(s.readLoopDone)
	defer close(s.events)
	for {
		mt, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if !s.isClosed() {
				var ce *websocket.CloseError
				if !errors.As(err, &ce) {
					s.setErr(err)
				}
				// *CloseError (server-initiated close) and a locally
				// initiated Close() both end the stream cleanly (nil
				// Err()); any other error (network failure, ctx
				// cancellation) is reported via Err().
			}
			return
		}
		if mt != websocket.TextMessage {
			continue // the Realtime API only sends JSON text messages
		}

		events, perr := parseRealtimeMessage(data)
		for _, e := range events {
			select {
			case s.events <- e:
			case <-s.ctx.Done():
				s.setErr(s.ctx.Err())
				return
			case <-s.closeCh:
				// Close() was called while an event was pending delivery
				// to a consumer that has stopped (or never started)
				// ranging over Events() — without this case, this send
				// would block forever, leaking the goroutine.
				return
			}
		}
		if perr != nil {
			s.setErr(perr)
			return
		}
	}
}

// oaWireMessage matches the OpenAI Realtime API event shapes relevant to
// transcription: incremental and final transcript events, and error
// events. Other event types (e.g. "session.created", "session.updated",
// "input_audio_buffer.committed") are ignored.
type oaWireMessage struct {
	Type       string `json:"type"`
	Delta      string `json:"delta"`
	Transcript string `json:"transcript"`
	Error      struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseRealtimeMessage decodes one OpenAI Realtime API event, returning any
// TranscriptEvents it produced and a terminal error (for "error" events, or
// a decode failure).
func parseRealtimeMessage(data []byte) (events []provider.TranscriptEvent, err error) {
	var msg oaWireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("openai: decode realtime message: %w", err)
	}

	switch msg.Type {
	case "conversation.item.input_audio_transcription.delta":
		if msg.Delta == "" {
			return nil, nil
		}
		return []provider.TranscriptEvent{{Text: msg.Delta, Final: false}}, nil
	case "conversation.item.input_audio_transcription.completed":
		return []provider.TranscriptEvent{{Text: msg.Transcript, Final: true}}, nil
	case "error":
		return nil, fmt.Errorf("openai: realtime error: %s", msg.Error.Message)
	default:
		return nil, nil
	}
}
