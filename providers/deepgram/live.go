package deepgram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket"
	"github.com/azrtydxb/go-ai-sdk/internal/wsstream"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// streamingTranscriptionModel implements provider.StreamingTranscriptionModel
// against Deepgram's /v1/listen live-streaming WebSocket endpoint.
type streamingTranscriptionModel struct {
	provider *Provider
	modelID  string
}

// StreamingTranscriptionModel returns a provider.StreamingTranscriptionModel
// for the given Deepgram model ID (e.g. "nova-3").
func (p *Provider) StreamingTranscriptionModel(id string) provider.StreamingTranscriptionModel {
	return &streamingTranscriptionModel{provider: p, modelID: id}
}

func (m *streamingTranscriptionModel) ModelID() string      { return m.modelID }
func (m *streamingTranscriptionModel) ProviderName() string { return providerName }

// StreamTranscribe dials Deepgram's live-transcription WebSocket endpoint
// and returns an open provider.TranscriptionStream.
//
// The dial URL is derived from the provider's configured baseURL by
// swapping http(s) for ws(s) — never hardcoded — so fixture servers set up
// via WithBaseURL work the same as the real Deepgram host.
//
// A "Results" message with an empty transcript is skipped entirely (no
// TranscriptEvent emitted) — including when it carries is_final:true — since
// Deepgram sends empty-transcript results for silence/non-speech audio and
// there is nothing useful to report for that segment.
func (m *streamingTranscriptionModel) StreamTranscribe(ctx context.Context, call provider.StreamTranscriptionCall) (provider.TranscriptionStream, error) {
	dialURL, err := buildDialURL(m.provider.baseURL, m.modelID, call)
	if err != nil {
		return nil, err
	}

	conn, err := websocket.Dial(ctx, dialURL, websocket.DialOptions{
		Header: http.Header{"Authorization": []string{"Token " + m.provider.apiKey}},
	})
	if err != nil {
		return nil, fmt.Errorf("deepgram: dial live transcription: %w", err)
	}

	ws := wsstream.New(wsstream.Config[provider.TranscriptEvent]{
		Ctx:    ctx,
		Conn:   conn,
		Decode: decodeDeepgramMessage,
	})
	return &liveStream{stream: ws, readLoopDone: ws.Done()}, nil
}

// buildDialURL derives the wss:// (or ws://, for test fixtures) URL for
// Deepgram's live-transcription endpoint from baseURL, deriving
// encoding/sample_rate query params from call.MediaType/call.SampleRate and
// merging call.ProviderOptions["deepgram"] as extra query params (same
// convention as the REST Transcribe path).
func buildDialURL(baseURL, modelID string, call provider.StreamTranscriptionCall) (string, error) {
	u, err := wsstream.DialURL(baseURL, "/v1/listen")
	if err != nil {
		return "", fmt.Errorf("deepgram: %w", err)
	}

	q := url.Values{}
	q.Set("model", modelID)
	if call.Language != "" {
		q.Set("language", call.Language)
	}
	if encoding, rate, ok := deepgramEncoding(call.MediaType, call.SampleRate); ok {
		q.Set("encoding", encoding)
		if rate > 0 {
			q.Set("sample_rate", strconv.Itoa(rate))
		}
	}
	addOptionsQuery(q, call.ProviderOptions)

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// deepgramEncoding maps a raw-audio MediaType (e.g. "audio/pcm;rate=16000")
// to Deepgram's encoding query param plus an effective sample rate (the
// MediaType's "rate" parameter if present, else sampleRateHint). ok is
// false for MediaTypes Deepgram doesn't need an explicit encoding for
// (e.g. "" or a container format like "audio/webm"), in which case the
// caller omits the encoding/sample_rate params entirely and lets Deepgram
// auto-detect from the stream.
func deepgramEncoding(mediaType string, sampleRateHint int) (encoding string, rate int, ok bool) {
	base := mediaType
	var params map[string]string
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		base = mediaType[:i]
		params = parseMediaTypeParams(mediaType[i+1:])
	}
	base = strings.ToLower(strings.TrimSpace(base))

	switch base {
	case "audio/pcm", "audio/l16":
		encoding = "linear16"
	default:
		return "", 0, false
	}

	rate = sampleRateHint
	if params != nil {
		if r, ok := params["rate"]; ok {
			if v, err := strconv.Atoi(r); err == nil {
				rate = v
			}
		}
	}
	return encoding, rate, true
}

// parseMediaTypeParams parses the "k=v; k2=v2" tail of a MediaType string
// into a map. Unlike mime.ParseMediaType, it tolerates the informal
// "rate=16000" parameter (not a real MIME parameter) without erroring.
func parseMediaTypeParams(tail string) map[string]string {
	params := map[string]string{}
	for _, part := range strings.Split(tail, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		params[strings.ToLower(strings.TrimSpace(kv[0]))] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
	}
	return params
}

// addOptionsQuery merges opts["deepgram"] into q as extra query parameters,
// using the same convention as the REST Transcribe path: a []any or
// []string value is added as one q.Add call per element (repeated query
// params), any other value is stringified and set with q.Set (overriding
// any SDK-set param of the same name).
func addOptionsQuery(q url.Values, opts map[string]any) {
	dgOpts, ok := opts["deepgram"].(map[string]any)
	if !ok {
		return
	}
	for k, v := range dgOpts {
		switch vv := v.(type) {
		case []any:
			q.Del(k)
			for _, item := range vv {
				q.Add(k, fmt.Sprint(item))
			}
		case []string:
			q.Del(k)
			for _, item := range vv {
				q.Add(k, item)
			}
		default:
			q.Set(k, fmt.Sprint(v))
		}
	}
}

// closeStreamMessage is the text frame Deepgram expects to signal
// end-of-audio and flush remaining results.
const closeStreamMessage = `{"type":"CloseStream"}`

// liveStream implements provider.TranscriptionStream against an open
// Deepgram live-transcription WebSocket connection, as a thin wrapper over
// the shared wsstream machinery (dial/readLoop/Close/Err/Events).
type liveStream struct {
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

// Send implements provider.TranscriptionStream by writing audio as a binary
// frame.
//
// It does not pre-check s.stream.Closed(): that check and the write below
// would be two independent acquisitions of the underlying wsstream.Stream's
// own lock, and a Close() landing in the gap between them would make the
// write below observe closed=true and return wsstream.ErrClosed --
// surfacing wsstream's own "wsstream: send called after close" instead of
// this method's documented error. Relying solely on the error
// stream.Send itself returns keeps the closed-check and the write atomic
// (both happen under the same, single lock acquisition inside
// stream.Send), so there is no such window.
func (s *liveStream) Send(ctx context.Context, audio []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closeSendSent {
		return errors.New("deepgram: Send called after CloseSend")
	}
	if err := s.stream.Send(ctx, websocket.BinaryMessage, audio); err != nil {
		if errors.Is(err, wsstream.ErrClosed) {
			return errors.New("deepgram: Send called after Close")
		}
		return err
	}
	return nil
}

// CloseSend implements provider.TranscriptionStream by sending Deepgram's
// {"type":"CloseStream"} text frame. Idempotent, including when the stream
// was already ended via Close (matching the pre-wsstream-migration
// contract): a wsstream.ErrClosed from the send below -- whether because
// CloseSend genuinely raced a concurrent Close, or because Close had
// already happened before this call -- is treated as the no-op success
// case, not surfaced as an error.
func (s *liveStream) CloseSend(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closeSendSent {
		return nil
	}
	s.closeSendSent = true
	if err := s.stream.Send(ctx, websocket.TextMessage, []byte(closeStreamMessage)); err != nil {
		if errors.Is(err, wsstream.ErrClosed) {
			return nil
		}
		return err
	}
	return nil
}

// Close implements provider.TranscriptionStream by aborting the connection
// without flushing, without waiting for outstanding events to be consumed.
// Idempotent. Because abort is caller-initiated rather than a stream
// failure, Err() reports nil once the stream ends as a result of Close
// (matching a peer-initiated clean close) rather than surfacing whatever
// error the now-closed connection produces on its next Read.
func (s *liveStream) Close() error {
	return s.stream.Close()
}

// Events implements provider.TranscriptionStream. Single use: it ranges
// over the channel the reader goroutine populates, which is closed exactly
// once when the stream ends.
func (s *liveStream) Events() iter.Seq[provider.TranscriptEvent] {
	return s.stream.Events()
}

// Err implements provider.TranscriptionStream.
func (s *liveStream) Err() error {
	return s.stream.Err()
}

// dgWireMessage matches Deepgram's live-transcription message shapes: a
// "Results" message carries a transcript segment, a "Metadata" message
// marks the end of the stream (sent after CloseStream is processed). Other
// message types (e.g. "SpeechStarted", "UtteranceEnd") are ignored.
type dgWireMessage struct {
	Type    string `json:"type"`
	Channel struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
		} `json:"alternatives"`
	} `json:"channel"`
	IsFinal  bool    `json:"is_final"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

// decodeDeepgramMessage is the wsstream.Config.Decode callback for a
// Deepgram live-transcription connection: it ignores non-text messages
// (Deepgram only sends JSON text messages) and otherwise decodes one
// message, returning any TranscriptEvents it produced, whether it marks a
// clean end of stream (a "Metadata" message), and a decode error if any.
func decodeDeepgramMessage(messageType int, data []byte) (events []provider.TranscriptEvent, terminal bool, err error) {
	if messageType != websocket.TextMessage {
		return nil, false, nil
	}
	var msg dgWireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, false, fmt.Errorf("deepgram: decode stream message: %w", err)
	}

	switch msg.Type {
	case "Results":
		if len(msg.Channel.Alternatives) == 0 {
			return nil, false, nil
		}
		transcript := msg.Channel.Alternatives[0].Transcript
		if transcript == "" {
			return nil, false, nil
		}
		return []provider.TranscriptEvent{{
			Text:     transcript,
			Final:    msg.IsFinal,
			StartSec: msg.Start,
			EndSec:   msg.Start + msg.Duration,
		}}, false, nil
	case "Metadata":
		return nil, true, nil
	default:
		return nil, false, nil
	}
}
