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

	"github.com/azrtydxb/go-ai-sdk/internal/websocket"
	"github.com/azrtydxb/go-ai-sdk/internal/wsstream"
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
// WebSocket connection, as a thin wrapper over the shared wsstream
// machinery (dial/readLoop/Close/Err/Events).
type RealtimeSession struct {
	stream *wsstream.Stream[RealtimeEvent]

	// readLoopDone mirrors stream.Done(): not part of the public
	// interface, but tests observe it directly to confirm the reader
	// goroutine has actually exited, not just that Events() stopped.
	readLoopDone <-chan struct{}
}

// RealtimeEvent is one event surfaced from an open RealtimeSession.
type RealtimeEvent struct {
	Type string // the raw server event type
	// AudioDelta is the base64-decoded payload for response.output_audio.delta
	// / response.audio.delta. If the server sends a delta that fails to
	// base64-decode, AudioDelta is left nil (indistinguishable from an
	// empty/absent delta) rather than failing the event or the session —
	// Raw still carries the full, undecoded event, so a caller that needs
	// to detect this case can inspect Raw's "delta" field directly.
	AudioDelta []byte
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

	ws := wsstream.New(wsstream.Config[RealtimeEvent]{
		Ctx:    ctx,
		Conn:   conn,
		Decode: decodeRealtimeVoiceMessage,
	})
	return &RealtimeSession{stream: ws, readLoopDone: ws.Done()}, nil
}

// realtimeVoiceDialURL derives the wss:// (or ws://, for test fixtures) URL
// for OpenAI's Realtime endpoint from baseURL, with model as a query
// parameter.
func realtimeVoiceDialURL(baseURL, model string) (string, error) {
	u, err := wsstream.DialURL(baseURL, "/realtime")
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
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
	if s.stream.Closed() {
		return errors.New("openai: SendAudio called after Close")
	}
	msg, err := json.Marshal(map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
	if err != nil {
		return fmt.Errorf("openai: encode input_audio_buffer.append: %w", err)
	}
	return s.stream.Send(ctx, websocket.TextMessage, msg)
}

// CommitAudio commits the input audio buffer via
// input_audio_buffer.commit.
func (s *RealtimeSession) CommitAudio(ctx context.Context) error {
	if s.stream.Closed() {
		return errors.New("openai: CommitAudio called after Close")
	}
	return s.stream.Send(ctx, websocket.TextMessage, []byte(`{"type":"input_audio_buffer.commit"}`))
}

// SendText sends a user text message via conversation.item.create.
func (s *RealtimeSession) SendText(ctx context.Context, text string) error {
	if s.stream.Closed() {
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
	return s.stream.Send(ctx, websocket.TextMessage, msg)
}

// CreateResponse requests a model response via response.create.
func (s *RealtimeSession) CreateResponse(ctx context.Context) error {
	if s.stream.Closed() {
		return errors.New("openai: CreateResponse called after Close")
	}
	return s.stream.Send(ctx, websocket.TextMessage, []byte(`{"type":"response.create"}`))
}

// Close aborts the connection without flushing, without waiting for
// outstanding events to be consumed. Idempotent. Because abort is
// caller-initiated rather than a session failure, Err() reports nil once
// the session ends as a result of Close (matching a peer-initiated clean
// close) rather than surfacing whatever error the now-closed connection
// produces on its next Read.
func (s *RealtimeSession) Close() error {
	return s.stream.Close()
}

// Events returns an iterator over the session's events. Single use: it
// ranges over the channel the reader goroutine populates, which is closed
// exactly once when the session ends.
func (s *RealtimeSession) Events() iter.Seq[RealtimeEvent] {
	return s.stream.Events()
}

// Err reports the error that ended the session, or nil if it ended
// cleanly (server close, or caller-initiated Close).
func (s *RealtimeSession) Err() error {
	return s.stream.Err()
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

// decodeRealtimeVoiceMessage is the wsstream.Config.Decode callback for an
// OpenAI Realtime voice session: it ignores non-text messages (the
// Realtime API only sends JSON text messages) and otherwise decodes one
// event into a RealtimeEvent. Unlike the transcription-stream Decode
// callbacks elsewhere in this package, a server "error" event does not end
// the session here — it is surfaced as a normal RealtimeEvent (terminal
// stays false) and the loop continues; only a decode failure of the outer
// event (returned as err) ends it, matching the socket-failure/ctx-
// cancellation/caller-Close paths wsstream itself already handles.
func decodeRealtimeVoiceMessage(messageType int, data []byte) (events []RealtimeEvent, terminal bool, err error) {
	if messageType != websocket.TextMessage {
		return nil, false, nil
	}

	var msg voiceWireEvent
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, false, fmt.Errorf("openai: decode realtime event: %w", err)
	}

	ev := RealtimeEvent{Type: msg.Type, Raw: json.RawMessage(data)}

	switch msg.Type {
	case "response.audio.delta", "response.output_audio.delta":
		if msg.Delta != "" {
			// A decode failure here is intentionally not propagated as an
			// error: it's a per-event data problem, not a connection
			// failure, and must not end the session (unlike the outer
			// json.Unmarshal above). Raw still carries the undecoded
			// "delta" field for a caller that needs to detect this case.
			if b, err := base64.StdEncoding.DecodeString(msg.Delta); err == nil {
				ev.AudioDelta = b
			}
		}
	case "response.audio_transcript.delta", "response.text.delta", "response.output_text.delta":
		ev.TextDelta = msg.Delta
	}

	return []RealtimeEvent{ev}, false, nil
}
