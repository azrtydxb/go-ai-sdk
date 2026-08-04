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
	"github.com/azrtydxb/go-ai-sdk/internal/wsstream"
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

	ws := wsstream.New(wsstream.Config[provider.TranscriptEvent]{
		Ctx:    ctx,
		Conn:   conn,
		Decode: decodeRealtimeTranscriptionMessage,
	})
	return &realtimeStream{stream: ws, readLoopDone: ws.Done()}, nil
}

// realtimeDialURL derives the wss:// (or ws://, for test fixtures) URL for
// OpenAI's Realtime endpoint in transcription-intent mode from baseURL.
func realtimeDialURL(baseURL string) (string, error) {
	u, err := wsstream.DialURL(baseURL, "/realtime")
	if err != nil {
		return "", fmt.Errorf("openai: %w", err)
	}
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

// audioFormatFor maps a MediaType to OpenAI's input_audio_format. OpenAI's
// Realtime API only supports "pcm16" as of this writing, so every
// recognized raw-PCM MediaType ("audio/pcm" or "audio/l16"; params such as
// ";rate=..." are ignored since the Realtime API always expects 24kHz mono
// PCM16 regardless) — and anything else, including an empty MediaType —
// all map to "pcm16". The switch stays explicit (rather than collapsing to
// an unconditional return) so a future second format is a one-line add,
// not a rewrite.
func audioFormatFor(mediaType string) string {
	base := mediaType
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		base = mediaType[:i]
	}
	base = strings.ToLower(strings.TrimSpace(base))
	switch base {
	case "audio/pcm", "audio/l16":
		return "pcm16"
	default:
		// Includes "" and any unrecognized MediaType: OpenAI's Realtime
		// API has no other supported input_audio_format today.
		return "pcm16"
	}
}

// realtimeStream implements provider.TranscriptionStream against an open
// OpenAI Realtime WebSocket connection in transcription mode, as a thin
// wrapper over the shared wsstream machinery
// (dial/readLoop/Close/Err/Events).
type realtimeStream struct {
	stream *wsstream.Stream[provider.TranscriptEvent]

	// writeMu serializes the closeSendSent guard below against concurrent
	// Send/CloseSend calls; the underlying conn write itself is separately
	// serialized against Close inside stream.Send.
	writeMu       sync.Mutex
	closeSendSent bool

	// readLoopDone mirrors stream.Done(): not part of the public
	// interface, but tests observe it directly to confirm the reader
	// goroutine has actually exited, not just that Events() stopped.
	readLoopDone <-chan struct{}
}

// Send implements provider.TranscriptionStream by base64-encoding audio
// into an input_audio_buffer.append event.
func (s *realtimeStream) Send(ctx context.Context, audio []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stream.Closed() {
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
	return s.stream.Send(ctx, websocket.TextMessage, msg)
}

// CloseSend implements provider.TranscriptionStream by sending
// input_audio_buffer.commit. Idempotent.
func (s *realtimeStream) CloseSend(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.stream.Closed() || s.closeSendSent {
		return nil
	}
	s.closeSendSent = true
	return s.stream.Send(ctx, websocket.TextMessage, []byte(`{"type":"input_audio_buffer.commit"}`))
}

// Close implements provider.TranscriptionStream by aborting the connection
// without flushing, without waiting for outstanding events to be consumed.
// Idempotent. Because abort is caller-initiated rather than a stream
// failure, Err() reports nil once the stream ends as a result of Close
// (matching a peer-initiated clean close) rather than surfacing whatever
// error the now-closed connection produces on its next Read.
func (s *realtimeStream) Close() error {
	return s.stream.Close()
}

// Events implements provider.TranscriptionStream. Single use: it ranges
// over the channel the reader goroutine populates, which is closed exactly
// once when the stream ends.
func (s *realtimeStream) Events() iter.Seq[provider.TranscriptEvent] {
	return s.stream.Events()
}

// Err implements provider.TranscriptionStream.
func (s *realtimeStream) Err() error {
	return s.stream.Err()
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

// decodeRealtimeTranscriptionMessage is the wsstream.Config.Decode callback
// for an OpenAI Realtime transcription connection: it ignores non-text
// messages (the Realtime API only sends JSON text messages) and otherwise
// decodes one event, returning any TranscriptEvents it produced. It never
// reports terminal=true: an "error" event (or a decode failure) ends the
// stream via the returned error instead, matching prior behavior where
// nothing about this stream type had a clean-terminal signal distinct from
// an error.
func decodeRealtimeTranscriptionMessage(messageType int, data []byte) (events []provider.TranscriptEvent, terminal bool, err error) {
	if messageType != websocket.TextMessage {
		return nil, false, nil
	}
	var msg oaWireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, false, fmt.Errorf("openai: decode realtime message: %w", err)
	}

	switch msg.Type {
	case "conversation.item.input_audio_transcription.delta":
		if msg.Delta == "" {
			return nil, false, nil
		}
		return []provider.TranscriptEvent{{Text: msg.Delta, Final: false}}, false, nil
	case "conversation.item.input_audio_transcription.completed":
		return []provider.TranscriptEvent{{Text: msg.Transcript, Final: true}}, false, nil
	case "error":
		return nil, false, fmt.Errorf("openai: realtime error: %s", msg.Error.Message)
	default:
		return nil, false, nil
	}
}
