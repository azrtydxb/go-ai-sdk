// Package wsstream provides the shared machinery behind a live,
// bidirectional WebSocket stream that yields decoded events: the
// read-pump goroutine, its clean-teardown and leak-safety guarantees, and
// idempotent Close/Err/Events semantics.
//
// It was extracted from three near-identical implementations (Deepgram's
// live-transcription stream, OpenAI's Realtime transcription stream, and
// OpenAI's Realtime voice session) that each independently reimplemented
// this ~150 lines of dial/readLoop/Close bookkeeping — duplication that
// already produced a real bug (a readLoop missing the conn-close teardown
// on a clean non-socket end). Centralizing it here means that fix, and the
// events-channel-send leak fix (see the closeCh case in readLoop), live in
// exactly one place.
//
// Each provider supplies a Decode callback for its own wire format and
// keeps its own public surface (event types, Send framing, provider-specific
// control messages, session-establishment handshake) as a thin wrapper over
// *Stream[E].
package wsstream

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/url"
	"strings"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/internal/websocket"
)

// ErrClosed is returned by Send once Close has been called.
var ErrClosed = errors.New("wsstream: send called after close")

// defaultEventBuffer is the events channel's buffer capacity when
// Config.EventBuffer is unset. It matches the buffer every prior
// per-provider WS stream used.
const defaultEventBuffer = 32

// Config drives one Stream, created by New.
type Config[E any] struct {
	// Ctx governs the reader goroutine's Conn.Read calls for the lifetime
	// of the stream (not just one call) — the same ctx a provider's
	// stream-constructing call (e.g. StreamTranscribe) received. Required;
	// New falls back to context.Background() if nil.
	Ctx context.Context
	// Conn is the already-dialed WebSocket connection to read/write. Any
	// provider-specific handshake (e.g. sending an initial session.update)
	// must happen before New is called, using Conn directly.
	Conn *websocket.Conn
	// Decode parses one incoming WS message into zero or more events plus
	// a terminal flag (a clean end without an error — e.g. Deepgram's
	// "Metadata" message) and a decode error (a non-nil error ends the
	// stream with that error). Decode is responsible for ignoring message
	// types/shapes it doesn't care about by returning (nil, false, nil).
	Decode func(messageType int, data []byte) (events []E, terminal bool, err error)
	// EventBuffer overrides the events channel's buffer capacity. <= 0
	// uses defaultEventBuffer (32).
	EventBuffer int
}

// Stream is a live, bidirectional WS session yielding decoded events of
// type E. Create one with New.
type Stream[E any] struct {
	ctx    context.Context // governs the reader goroutine's Read calls
	conn   *websocket.Conn
	decode func(messageType int, data []byte) ([]E, bool, error)

	events chan E

	// closeCh is closed exactly once, by Close(), to unblock readLoop if
	// it's parked trying to send a buffered event to a consumer that has
	// already stopped ranging over Events() without cancelling ctx.
	closeCh chan struct{}
	// readLoopDone is closed when readLoop returns, after events is
	// closed. Exposed via Done so a provider wrapper (and its tests) can
	// observe that the reader goroutine has actually exited, rather than
	// just that events was closed.
	readLoopDone chan struct{}

	mu     sync.Mutex // serializes Send/Close against each other (conn write + closed-flag contract)
	closed bool

	errMu sync.Mutex
	err   error
}

// New starts the reader goroutine against cfg.Conn and returns the open
// Stream.
func New[E any](cfg Config[E]) *Stream[E] {
	buf := cfg.EventBuffer
	if buf <= 0 {
		buf = defaultEventBuffer
	}
	ctx := cfg.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s := &Stream[E]{
		ctx:          ctx,
		conn:         cfg.Conn,
		decode:       cfg.Decode,
		events:       make(chan E, buf),
		closeCh:      make(chan struct{}),
		readLoopDone: make(chan struct{}),
	}
	go s.readLoop()
	return s
}

// Send writes one WS message (messageType is websocket.TextMessage or
// websocket.BinaryMessage), synchronized against Close. Returns ErrClosed
// if Close has already been called.
func (s *Stream[E]) Send(ctx context.Context, messageType int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if messageType == websocket.BinaryMessage {
		return s.conn.WriteBinary(ctx, data)
	}
	return s.conn.WriteText(ctx, data)
}

// Close aborts the connection without flushing, without waiting for
// outstanding events to be consumed. Idempotent. Because the abort is
// caller-initiated rather than a stream failure, Err() reports nil once the
// stream ends as a result of Close (matching a peer-initiated clean close)
// rather than surfacing whatever error the now-closed connection produces
// on its next Read.
func (s *Stream[E]) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.conn.Close(websocket.CloseNormal, "")
}

// Closed reports whether Close has been called.
func (s *Stream[E]) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Events returns an iterator over the stream's events. Single use: it
// ranges over the channel the reader goroutine populates, which is closed
// exactly once when the stream ends.
func (s *Stream[E]) Events() iter.Seq[E] {
	return func(yield func(E) bool) {
		for e := range s.events {
			if !yield(e) {
				return
			}
		}
	}
}

// Err reports the error that ended the stream, or nil if it ended cleanly
// (a terminal Decode result, a peer close, or a caller-initiated Close).
func (s *Stream[E]) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *Stream[E]) setErr(err error) {
	s.errMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.errMu.Unlock()
}

// Done returns a channel that's closed once the reader goroutine has
// exited (after the Events() channel is closed). Exposed for provider
// wrappers whose tests need to observe the reader goroutine's actual exit,
// not just that Events() has stopped yielding.
func (s *Stream[E]) Done() <-chan struct{} {
	return s.readLoopDone
}

// readLoop pumps conn.Read through Decode into s.events until the stream
// ends, then closes s.events. It runs in its own goroutine, started by New.
func (s *Stream[E]) readLoop() {
	defer close(s.readLoopDone)
	defer close(s.events)
	// On a clean non-socket end (a terminal Decode result, or a
	// decode/error terminal) the underlying conn is still open until
	// something closes it. Without this, the TCP connection lingers until
	// the caller eventually calls Close() (if ever) instead of being torn
	// down as soon as the stream logically ends. Conn.Close is idempotent,
	// so this is a no-op on the paths that already shut the conn down
	// themselves (a *websocket.CloseError or an abnormal closure).
	defer s.conn.Close(websocket.CloseNormal, "")
	for {
		mt, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if !s.Closed() {
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

		events, terminal, perr := s.decode(mt, data)
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

// DialURL derives a ws:// (or wss://) URL from baseURL by swapping the
// http(s) scheme, then appends path to baseURL's path (trailing slashes
// trimmed first). Every provider's dial-URL helper applied this same rule
// so that fixture servers set up via an http(s) WithBaseURL work the same
// as the real wss:// host. The caller sets any query parameters on the
// returned URL before dialing.
func DialURL(baseURL, path string) (*url.URL, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("wsstream: parse base URL: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return nil, fmt.Errorf("wsstream: unsupported base URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u, nil
}
