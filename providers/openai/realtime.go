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
)

// RealtimeConfig configures a RealtimeSession dialed via
// Provider.RealtimeSession.
//
// This is OpenAI-specific: there is no generic provider.RealtimeModel
// interface this wave, and RealtimeSession is not wired into ai.Registry
// (niche modality) — construct it directly against an *openai.Provider.
type RealtimeConfig struct {
	Model             string // e.g. "gpt-4o-realtime-preview"
	Voice             string
	Instructions      string
	InputAudioFormat  string // "pcm16" default
	OutputAudioFormat string // "pcm16" default
	// ProviderOptions is merged into session.update's session object, each
	// entry winning over whatever RealtimeConfig's other fields set for
	// that key.
	ProviderOptions map[string]any
}

// RealtimeSession is an open OpenAI Realtime API voice session over a
// WebSocket connection.
type RealtimeSession struct {
	ctx  context.Context // governs the reader goroutine's Read calls
	conn *websocket.Conn

	events chan RealtimeEvent

	// closeCh is closed exactly once, by Close(), to unblock readLoop if
	// it's parked trying to send a buffered event to a consumer that has
	// already stopped ranging over Events() without cancelling ctx.
	closeCh chan struct{}
	// readLoopDone is closed when readLoop returns, after events is
	// closed. Not part of the public interface; exists so tests (and
	// Close, defensively) can observe that the reader goroutine has
	// actually exited rather than just that events was closed.
	readLoopDone chan struct{}

	writeMu sync.Mutex // serializes Send*/CreateResponse/Close (conn write contract)
	closed  bool

	errMu sync.Mutex
	err   error
}

// RealtimeEvent is one event surfaced from an open RealtimeSession.
type RealtimeEvent struct {
	Type       string          // the raw server event type
	AudioDelta []byte          // decoded, for response.output_audio.delta / response.audio.delta
	TextDelta  string          // response.output_text.delta / response.text.delta / audio transcript deltas
	Raw        json.RawMessage // always the full event
}

// RealtimeSession dials OpenAI's Realtime WebSocket endpoint, sends the
// initial session.update built from cfg, and returns an open
// *RealtimeSession.
//
// The dial URL is derived from the provider's configured baseURL by
// swapping http(s) for ws(s) — never hardcoded — so fixture servers set up
// via WithBaseURL work the same as the real OpenAI host.
func (p *Provider) RealtimeSession(ctx context.Context, cfg RealtimeConfig) (*RealtimeSession, error) {
	dialURL, err := realtimeVoiceDialURL(p.baseURL, cfg.Model)
	if err != nil {
		return nil, err
	}

	conn, err := websocket.Dial(ctx, dialURL, websocket.DialOptions{
		Header: http.Header{
			"Authorization": []string{"Bearer " + p.apiKey},
			"OpenAI-Beta":   []string{"realtime=v1"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("openai: dial realtime session: %w", err)
	}

	sessionUpdate, err := buildRealtimeSessionUpdate(cfg)
	if err != nil {
		conn.Close(websocket.CloseNormal, "")
		return nil, err
	}
	if err := conn.WriteText(ctx, sessionUpdate); err != nil {
		conn.Close(websocket.CloseNormal, "")
		return nil, fmt.Errorf("openai: send session.update: %w", err)
	}

	s := &RealtimeSession{
		ctx:          ctx,
		conn:         conn,
		events:       make(chan RealtimeEvent, 32),
		closeCh:      make(chan struct{}),
		readLoopDone: make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// realtimeVoiceDialURL derives the wss:// (or ws://, for test fixtures) URL
// for OpenAI's Realtime endpoint from baseURL, with model as a query
// parameter.
func realtimeVoiceDialURL(baseURL, model string) (string, error) {
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
	q.Set("model", model)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// buildRealtimeSessionUpdate builds the session.update message sent as
// soon as the socket is open, omitting empty RealtimeConfig fields and
// merging cfg.ProviderOptions["openai"] over them.
func buildRealtimeSessionUpdate(cfg RealtimeConfig) ([]byte, error) {
	session := map[string]any{}
	if cfg.Voice != "" {
		session["voice"] = cfg.Voice
	}
	if cfg.Instructions != "" {
		session["instructions"] = cfg.Instructions
	}
	if cfg.InputAudioFormat != "" {
		session["input_audio_format"] = cfg.InputAudioFormat
	}
	if cfg.OutputAudioFormat != "" {
		session["output_audio_format"] = cfg.OutputAudioFormat
	}
	if opts, ok := cfg.ProviderOptions["openai"].(map[string]any); ok {
		for k, v := range opts {
			session[k] = v
		}
	}

	msg := map[string]any{
		"type":    "session.update",
		"session": session,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("openai: encode session.update: %w", err)
	}
	return data, nil
}

// SendAudio appends audio to the input buffer via
// input_audio_buffer.append, base64-encoding it first.
func (s *RealtimeSession) SendAudio(ctx context.Context, audio []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("openai: SendAudio called after Close")
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

// CommitAudio commits the input audio buffer via
// input_audio_buffer.commit.
func (s *RealtimeSession) CommitAudio(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("openai: CommitAudio called after Close")
	}
	return s.conn.WriteText(ctx, []byte(`{"type":"input_audio_buffer.commit"}`))
}

// SendText sends a user text message via conversation.item.create.
func (s *RealtimeSession) SendText(ctx context.Context, text string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("openai: SendText called after Close")
	}
	msg, err := json.Marshal(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": text},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("openai: encode conversation.item.create: %w", err)
	}
	return s.conn.WriteText(ctx, msg)
}

// CreateResponse requests a model response via response.create.
func (s *RealtimeSession) CreateResponse(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("openai: CreateResponse called after Close")
	}
	return s.conn.WriteText(ctx, []byte(`{"type":"response.create"}`))
}

// Close aborts the connection without flushing, without waiting for
// outstanding events to be consumed. Idempotent. Because abort is
// caller-initiated rather than a session failure, Err() reports nil once
// the session ends as a result of Close (matching a peer-initiated clean
// close) rather than surfacing whatever error the now-closed connection
// produces on its next Read.
func (s *RealtimeSession) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.conn.Close(websocket.CloseNormal, "")
}

func (s *RealtimeSession) isClosed() bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.closed
}

// Events returns an iterator over the session's events. Single use: it
// ranges over the channel the reader goroutine populates, which is closed
// exactly once when the session ends.
func (s *RealtimeSession) Events() iter.Seq[RealtimeEvent] {
	return func(yield func(RealtimeEvent) bool) {
		for e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

// Err reports the error that ended the session, or nil if it ended
// cleanly (server close, or caller-initiated Close).
func (s *RealtimeSession) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *RealtimeSession) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

// readLoop pumps conn.Read into s.events until the session ends, then
// closes s.events. It runs in its own goroutine, started by
// RealtimeSession's constructor. Unlike the transcription-stream readLoops
// elsewhere in this package, a server "error" event does not end
// iteration here — it is surfaced as a normal RealtimeEvent and the loop
// continues; only a socket failure (or ctx cancellation, or a
// caller-initiated Close) ends the session.
func (s *RealtimeSession) readLoop() {
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
				// initiated Close() both end the session cleanly (nil
				// Err()); any other error (network failure, ctx
				// cancellation) is reported via Err().
			}
			return
		}
		if mt != websocket.TextMessage {
			continue // the Realtime API only sends JSON text messages
		}

		ev, perr := parseRealtimeVoiceEvent(data)
		if perr != nil {
			s.setErr(perr)
			return
		}

		select {
		case s.events <- ev:
		case <-s.ctx.Done():
			s.setErr(s.ctx.Err())
			return
		case <-s.closeCh:
			// Close() was called while an event was pending delivery to a
			// consumer that has stopped (or never started) ranging over
			// Events() — without this case, this send would block
			// forever, leaking the goroutine.
			return
		}
	}
}

// voiceWireEvent matches the OpenAI Realtime API event shapes relevant to
// a voice session: audio/text delta events (both the old and new naming)
// and error events. Every other event type (e.g. "session.created",
// "session.updated", "response.done", "input_audio_buffer.committed") is
// still surfaced as a RealtimeEvent with only Type and Raw set.
type voiceWireEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

// parseRealtimeVoiceEvent decodes one OpenAI Realtime API event into a
// RealtimeEvent. Raw is always set to the full event; AudioDelta/TextDelta
// are populated only for the known delta event types. A decode failure is
// the only case that returns an error.
func parseRealtimeVoiceEvent(data []byte) (RealtimeEvent, error) {
	var msg voiceWireEvent
	if err := json.Unmarshal(data, &msg); err != nil {
		return RealtimeEvent{}, fmt.Errorf("openai: decode realtime event: %w", err)
	}

	ev := RealtimeEvent{Type: msg.Type, Raw: json.RawMessage(data)}

	switch msg.Type {
	case "response.audio.delta", "response.output_audio.delta":
		if msg.Delta != "" {
			if b, err := base64.StdEncoding.DecodeString(msg.Delta); err == nil {
				ev.AudioDelta = b
			}
		}
	case "response.audio_transcript.delta", "response.text.delta", "response.output_text.delta":
		ev.TextDelta = msg.Delta
	}

	return ev, nil
}
