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

	s := &liveStream{
		ctx:          ctx,
		conn:         conn,
		events:       make(chan provider.TranscriptEvent, 32),
		closeCh:      make(chan struct{}),
		readLoopDone: make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// buildDialURL derives the wss:// (or ws://, for test fixtures) URL for
// Deepgram's live-transcription endpoint from baseURL, deriving
// encoding/sample_rate query params from call.MediaType/call.SampleRate and
// merging call.ProviderOptions["deepgram"] as extra query params (same
// convention as the REST Transcribe path).
func buildDialURL(baseURL, modelID string, call provider.StreamTranscriptionCall) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("deepgram: parse base URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("deepgram: unsupported base URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/listen"

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
// Deepgram live-transcription WebSocket connection.
type liveStream struct {
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

// Send implements provider.TranscriptionStream by writing audio as a binary
// frame.
func (s *liveStream) Send(ctx context.Context, audio []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return errors.New("deepgram: Send called after Close")
	}
	if s.closeSendSent {
		return errors.New("deepgram: Send called after CloseSend")
	}
	return s.conn.WriteBinary(ctx, audio)
}

// CloseSend implements provider.TranscriptionStream by sending Deepgram's
// {"type":"CloseStream"} text frame. Idempotent.
func (s *liveStream) CloseSend(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed || s.closeSendSent {
		return nil
	}
	s.closeSendSent = true
	return s.conn.WriteText(ctx, []byte(closeStreamMessage))
}

// Close implements provider.TranscriptionStream by aborting the connection
// without flushing, without waiting for outstanding events to be consumed.
// Idempotent. Because abort is caller-initiated rather than a stream
// failure, Err() reports nil once the stream ends as a result of Close
// (matching a peer-initiated clean close) rather than surfacing whatever
// error the now-closed connection produces on its next Read.
func (s *liveStream) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.conn.Close(websocket.CloseNormal, "")
}

func (s *liveStream) isClosed() bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.closed
}

// Events implements provider.TranscriptionStream. Single use: it ranges
// over the channel the reader goroutine populates, which is closed exactly
// once when the stream ends.
func (s *liveStream) Events() iter.Seq[provider.TranscriptEvent] {
	return func(yield func(provider.TranscriptEvent) bool) {
		for e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

// Err implements provider.TranscriptionStream.
func (s *liveStream) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *liveStream) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

// readLoop pumps conn.Read into s.events until the stream ends, then closes
// s.events. It runs in its own goroutine, started by StreamTranscribe.
func (s *liveStream) readLoop() {
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
			continue // Deepgram only sends JSON text messages
		}

		events, terminal, perr := parseDeepgramMessage(data)
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
		if terminal {
			return
		}
	}
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

// parseDeepgramMessage decodes one Deepgram live-transcription message,
// returning any TranscriptEvents it produced, whether it marks a clean end
// of stream (a "Metadata" message), and a decode error if any.
func parseDeepgramMessage(data []byte) (events []provider.TranscriptEvent, terminal bool, err error) {
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
