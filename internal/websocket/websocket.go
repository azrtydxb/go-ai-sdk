// Package websocket implements an RFC 6455 WebSocket client using only the
// Go standard library.
//
// Scope is deliberately narrow: this is a *client* only (no server-side
// handshake acceptance in the main package — that lives in the sibling
// websockettest package for test/fixture use), there is no support for
// negotiating extensions (e.g. permessage-deflate) or subprotocols, and
// received text message payloads are not validated as UTF-8 (RFC 6455
// §8.1 would require dropping the connection on invalid UTF-8, but every
// caller of this internal package parses the payload as JSON, which fails
// naturally on malformed text, so the extra validation pass is not worth
// the cost here).
//
// Concurrency contract: Read must only be called from one goroutine at a
// time, and WriteText/WriteBinary from at most one other goroutine at a
// time; one reader concurrent with one writer is safe. Control frames
// (ping/pong/close) generated internally share the writer's lock, so they
// never tear a user write.
package websocket

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Message types returned by Conn.Read and accepted by WriteText/WriteBinary,
// matching the RFC 6455 §11.8 data opcodes.
const (
	TextMessage   = 1
	BinaryMessage = 2
)

// Close status codes, RFC 6455 §7.4.1.
const (
	CloseNormal        = 1000
	CloseGoingAway     = 1001
	CloseProtocolError = 1002
	// CloseAbnormal is never sent on the wire; Read synthesizes it (in a
	// *CloseError) when the connection drops without a close handshake.
	CloseAbnormal = 1006
)

// closeMessageTooBig and closeNoStatus are close codes used internally that
// are not part of the exported API surface (callers only ever observe them
// via *CloseError.Code or a returned error).
const (
	closeMessageTooBig = 1009
	closeNoStatus      = 1005
)

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// defaultMaxMessageBytes is applied when DialOptions.MaxMessageBytes is 0.
const defaultMaxMessageBytes = 16 * 1024 * 1024

// controlWriteTimeout bounds writeControl's write of a control frame
// (notably the automatic pong Read sends in response to a ping). Without
// this, a peer that pings and then stops draining the socket could wedge a
// caller's Read indefinitely on the outgoing pong write — a path ctx
// cancellation can't reach, since that write happens deep inside Read,
// scoped only to the read-direction deadline. This is a fixed, short bound
// rather than derived from the caller's ctx: writeControl runs on the read
// path (and may run with context.Background(), which has no deadline of its
// own to borrow) and must not block the connection indefinitely regardless
// of what ctx the caller passed to Read.
const controlWriteTimeout = 10 * time.Second

// DialOptions configures Dial.
type DialOptions struct {
	// Header carries extra handshake request headers (e.g. Authorization).
	// Entries named Host, Upgrade, Connection, or Sec-WebSocket-* are
	// reserved (the handshake sets them itself) and are silently skipped.
	// A header name or value containing a CR or LF makes Dial return an
	// error rather than risk request-splitting.
	Header http.Header
	// TLSConfig configures wss:// connections; nil uses a default config.
	TLSConfig *tls.Config
	// NetDial overrides how the TCP connection is established; nil uses
	// &net.Dialer{}.
	NetDial func(ctx context.Context, network, addr string) (net.Conn, error)
	// MaxMessageBytes caps the reassembled size of one message; 0 means
	// 16 MiB.
	MaxMessageBytes int
}

// CloseError is returned by Conn.Read once the close handshake has
// completed (either the peer initiated it, or the connection was dropped
// abnormally, in which case Code is CloseAbnormal).
type CloseError struct {
	Code   int
	Reason string
}

// Error implements the error interface.
func (e *CloseError) Error() string {
	return fmt.Sprintf("websocket: closed: code=%d reason=%q", e.Code, e.Reason)
}

// Conn is an open RFC 6455 WebSocket client connection. Create one with
// Dial. The zero value is not usable.
type Conn struct {
	conn       net.Conn
	br         *bufio.Reader
	maxMessage int

	writeMu        sync.Mutex
	closeFrameOnce sync.Once
	shutdownOnce   sync.Once
	localClose     atomic.Bool
}

// Dial performs the RFC 6455 client opening handshake against a ws:// or
// wss:// URL and returns an open connection. ctx governs both the TCP dial
// and the HTTP handshake; once Dial returns, ctx no longer affects the
// connection (each Read/Write call takes its own ctx).
func Dial(ctx context.Context, wsURL string, opts DialOptions) (*Conn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, fmt.Errorf("websocket: parse URL: %w", err)
	}

	var useTLS bool
	switch u.Scheme {
	case "ws":
		useTLS = false
	case "wss":
		useTLS = true
	default:
		return nil, fmt.Errorf("websocket: unsupported scheme %q (want ws or wss)", u.Scheme)
	}

	hostname := u.Hostname()
	port := u.Port()
	if port == "" {
		if useTLS {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(hostname, port)

	netDial := opts.NetDial
	if netDial == nil {
		d := &net.Dialer{}
		netDial = d.DialContext
	}

	rawConn, err := netDial(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("websocket: dial: %w", err)
	}

	conn := net.Conn(rawConn)
	if useTLS {
		tlsConfig := opts.TLSConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		}
		if tlsConfig.ServerName == "" {
			tlsConfig = tlsConfig.Clone()
			tlsConfig.ServerName = hostname
		}
		tlsConn := tls.Client(rawConn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("websocket: tls handshake: %w", err)
		}
		conn = tlsConn
	}

	var result *Conn
	err = runWithContext(ctx, conn, dirBoth, func() error {
		c, herr := handshake(conn, u, opts.Header)
		result = c
		return herr
	})
	if err != nil {
		conn.Close()
		return nil, err
	}

	maxMsg := opts.MaxMessageBytes
	if maxMsg <= 0 {
		maxMsg = defaultMaxMessageBytes
	}
	result.maxMessage = maxMsg
	return result, nil
}

// handshake sends the HTTP upgrade request over conn and validates the
// server's response, returning a *Conn wrapping conn and the buffered
// reader used to read the response (which may already contain the start of
// the first frame).
func handshake(conn net.Conn, u *url.URL, extraHeaders http.Header) (*Conn, error) {
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("websocket: generate Sec-WebSocket-Key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	var req bytes.Buffer
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", u.RequestURI())
	fmt.Fprintf(&req, "Host: %s\r\n", u.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", key)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range extraHeaders {
		if isReservedHeader(name) {
			// Host/Upgrade/Connection/Sec-WebSocket-* are set above and
			// must not be overridden by caller-supplied headers.
			continue
		}
		if strings.ContainsAny(name, "\r\n") {
			return nil, fmt.Errorf("websocket: invalid header name %q: contains CR or LF", name)
		}
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				return nil, fmt.Errorf("websocket: invalid value for header %q: contains CR or LF", name)
			}
			fmt.Fprintf(&req, "%s: %s\r\n", name, v)
		}
	}
	req.WriteString("\r\n")

	if _, err := conn.Write(req.Bytes()); err != nil {
		return nil, fmt.Errorf("websocket: send handshake request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		return nil, fmt.Errorf("websocket: read handshake response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("websocket: handshake failed: status %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	if !headerHasToken(resp.Header.Get("Connection"), "upgrade") ||
		!strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("websocket: handshake failed: missing or invalid Upgrade/Connection headers")
	}
	if want := computeAccept(key); resp.Header.Get("Sec-WebSocket-Accept") != want {
		return nil, errors.New("websocket: handshake failed: Sec-WebSocket-Accept mismatch")
	}

	return &Conn{conn: conn, br: br}, nil
}

// isReservedHeader reports whether name collides with a header the
// handshake sets itself (Host, Upgrade, Connection, or any
// Sec-WebSocket-* header). DialOptions.Header entries with these names
// are silently skipped rather than overriding the protocol-required
// values.
func isReservedHeader(name string) bool {
	switch {
	case strings.EqualFold(name, "Host"),
		strings.EqualFold(name, "Upgrade"),
		strings.EqualFold(name, "Connection"):
		return true
	}
	return len(name) >= len("Sec-WebSocket-") && strings.EqualFold(name[:len("Sec-WebSocket-")], "Sec-WebSocket-")
}

// headerHasToken reports whether value (a comma-separated header value, as
// used by the Connection header) contains token, case-insensitively.
func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// computeAccept derives the expected Sec-WebSocket-Accept value from the
// client's Sec-WebSocket-Key, per RFC 6455 §1.3.
func computeAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsMagic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ioDirection tells runWithContext which half of a full-duplex conn a call
// affects, so its deadline poisoning/reset touches only that axis.
type ioDirection int

const (
	// dirRead scopes runWithContext to SetReadDeadline only (Conn.Read).
	dirRead ioDirection = iota
	// dirWrite scopes runWithContext to SetWriteDeadline only
	// (WriteText/WriteBinary and the control frames sharing writeMu).
	dirWrite
	// dirBoth scopes runWithContext to SetDeadline (both halves) — used
	// only by the handshake, which both writes the upgrade request and
	// reads the response over the same still-shared conn before Read/Write
	// have split responsibility for their own deadlines.
	dirBoth
)

// setDeadline applies t to conn, scoped to dir.
func setDeadline(conn net.Conn, dir ioDirection, t time.Time) {
	switch dir {
	case dirRead:
		_ = conn.SetReadDeadline(t)
	case dirWrite:
		_ = conn.SetWriteDeadline(t)
	default:
		_ = conn.SetDeadline(t)
	}
}

// runWithContext runs fn, arranging for a blocked conn I/O call inside fn
// to be interrupted (via a deadline scoped to dir) if ctx is done before fn
// returns. If ctx has no deadline/cancellation (e.g. context.Background()),
// fn runs with no extra overhead.
//
// runWithContext always waits for the watcher goroutine to fully finish
// touching conn's deadline before returning — this closes a race where a
// ctx that fires right as fn is finishing could otherwise set a deadline
// on conn *after* control had already returned to the caller (e.g. into
// the very next Read/Write call, or into a `defer cancel()` that races
// this function's own cleanup), permanently poisoning an otherwise-healthy
// connection.
//
// If fn failed because the watcher's deadline actually interrupted it,
// the returned error is ctx.Err() and, per Read/Write's documented
// contract, the connection is left unusable (deadline still in the past)
// for that direction. If the watcher fired but fn nonetheless succeeded (a
// benign race between the ctx deadline and the underlying I/O completing),
// the deadline is reset so the connection remains usable — ctx
// cancellation must not fail an operation that actually completed.
//
// dir scopes every deadline touch (poison and reset) to one half of the
// full-duplex conn: dirRead only ever calls SetReadDeadline, dirWrite only
// ever calls SetWriteDeadline. This matters because a plain SetDeadline is
// conn-global — without this scoping, a concurrent Read and Write on the
// same Conn (an explicitly supported usage pattern) could otherwise have
// one call's cleanup silently erase the deadline the other call's watcher
// had just set to interrupt it, leaving that other call blocked forever
// with no deadline and no watcher left to fix it. dirBoth preserves the
// old conn-global behavior for call sites (the handshake) that
// legitimately both read and write before Read/Write's own per-direction
// contracts apply.
func runWithContext(ctx context.Context, conn net.Conn, dir ioDirection, fn func() error) error {
	if ctx.Done() == nil {
		return fn()
	}

	stop := make(chan struct{})
	watcherDone := make(chan struct{})
	var interrupted atomic.Bool
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			interrupted.Store(true)
			setDeadline(conn, dir, time.Unix(0, 0))
		case <-stop:
		}
	}()

	err := fn()
	close(stop)
	<-watcherDone // don't return until the watcher can no longer touch the deadline unexpectedly

	if interrupted.Load() {
		if err == nil {
			// fn completed despite the ctx firing; don't leave a stale
			// past deadline on an otherwise-healthy connection.
			setDeadline(conn, dir, time.Time{})
		} else {
			return ctx.Err()
		}
	}
	return err
}

// Read returns the next complete data message (reassembling fragments).
// Control frames are handled internally: pings are answered with pongs,
// pongs are ignored, and a close frame completes the closing handshake and
// returns *CloseError. ctx cancels a blocked read (the connection is then
// unusable). Read must only be called from one goroutine.
func (c *Conn) Read(ctx context.Context) (messageType int, data []byte, err error) {
	err = runWithContext(ctx, c.conn, dirRead, func() error {
		mt, d, rerr := c.readMessage()
		messageType, data = mt, d
		return rerr
	})
	return messageType, data, err
}

func (c *Conn) readMessage() (int, []byte, error) {
	var (
		fragmenting bool
		msgOpcode   uint8
		buf         []byte
	)

	for {
		h, err := readFrameHeader(c.br)
		if err != nil {
			return 0, nil, c.handleFrameError(err)
		}

		if h.masked {
			c.abort(CloseProtocolError, "server frame must not be masked")
			return 0, nil, errors.New("websocket: received masked frame from server")
		}

		switch {
		case isControlOpcode(h.opcode):
			payload, err := readFramePayload(c.br, h)
			if err != nil {
				return 0, nil, c.handleFrameError(err)
			}
			switch h.opcode {
			case opPing:
				if werr := c.writeControl(opPong, payload); werr != nil {
					c.shutdown()
					return 0, nil, werr
				}
			case opPong:
				// ignored
			case opClose:
				if len(payload) == 1 {
					// RFC 6455 §7.4.1: a close payload carries either no
					// status code (0 bytes) or a 2-byte code plus optional
					// reason (>=2 bytes) — exactly 1 byte is not a valid
					// encoding of either. Abort rather than echo it back.
					c.abort(CloseProtocolError, "invalid close frame payload length")
					return 0, nil, errors.New("websocket: invalid close frame payload length (1 byte)")
				}
				code, reason := parseClosePayload(payload)
				c.closeFrameOnce.Do(func() {
					// Echo back exactly what the peer sent. In particular,
					// if the peer's close carried no status code (payload
					// too short — parsed as closeNoStatus/1005 below),
					// echo an equally empty payload: RFC 6455 §7.4.1
					// forbids ever sending 1005 on the wire, even though
					// CloseError.Code below still reports it to the caller.
					_ = c.writeControl(opClose, payload)
				})
				c.shutdown()
				return 0, nil, &CloseError{Code: code, Reason: reason}
			default:
				c.abort(CloseProtocolError, "unknown control opcode")
				return 0, nil, fmt.Errorf("websocket: unknown control opcode %d", h.opcode)
			}

		case h.opcode == opText || h.opcode == opBinary:
			if fragmenting {
				c.abort(CloseProtocolError, "new data frame received mid-fragmentation")
				return 0, nil, errors.New("websocket: new data frame received mid-fragmentation")
			}
			// Check the *declared* length against budget before reading
			// any payload bytes: a server can lie about the length, and
			// reading it first would risk an oversized allocation (or a
			// panic on a bogus 64-bit length) or a read that blocks
			// forever waiting for bytes the server never sends.
			if err := c.checkFrameBudget(0, h.payloadLen); err != nil {
				return 0, nil, err
			}
			payload, err := readFramePayload(c.br, h)
			if err != nil {
				return 0, nil, c.handleFrameError(err)
			}
			if h.fin {
				return int(h.opcode), payload, nil
			}
			fragmenting = true
			msgOpcode = h.opcode
			buf = append([]byte(nil), payload...)

		case h.opcode == opContinuation:
			if !fragmenting {
				c.abort(CloseProtocolError, "continuation frame with nothing to continue")
				return 0, nil, errors.New("websocket: continuation frame with nothing to continue")
			}
			if err := c.checkFrameBudget(len(buf), h.payloadLen); err != nil {
				return 0, nil, err
			}
			payload, err := readFramePayload(c.br, h)
			if err != nil {
				return 0, nil, c.handleFrameError(err)
			}
			buf = append(buf, payload...)
			if h.fin {
				return int(msgOpcode), buf, nil
			}

		default:
			c.abort(CloseProtocolError, "unknown opcode")
			return 0, nil, fmt.Errorf("websocket: unknown opcode %d", h.opcode)
		}
	}
}

// checkFrameBudget enforces MaxMessageBytes across a message being
// reassembled from one or more frames. It is checked against a frame's
// declared length (already is the size accumulated from prior fragments)
// before that frame's payload is read off the wire.
func (c *Conn) checkFrameBudget(already int, frameLen uint64) error {
	if c.maxMessage <= 0 {
		return nil
	}
	remaining := c.maxMessage - already
	if remaining < 0 {
		remaining = 0
	}
	if frameLen > uint64(remaining) {
		c.abort(closeMessageTooBig, "message too large")
		return fmt.Errorf("websocket: message exceeds MaxMessageBytes (%d)", c.maxMessage)
	}
	return nil
}

// handleFrameError classifies a frame-read failure. A protocolErr (raised
// by readFrameHeader for a low-level framing violation, e.g. bad reserved
// bits or an oversized control frame) is turned into a proper 1002 abort —
// sending the close frame and shutting down the socket — instead of just
// being handed back to the caller. An EOF not preceded by a local Close()
// is an abnormal closure (synthesized 1006); anything else (including a
// ctx-driven deadline, unwound by the caller) is returned as-is.
func (c *Conn) handleFrameError(err error) error {
	var perr protocolErr
	if errors.As(err, &perr) {
		c.abort(CloseProtocolError, string(perr))
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if c.localClose.Load() {
			return err
		}
		c.shutdown()
		return &CloseError{Code: CloseAbnormal, Reason: "abnormal closure: " + err.Error()}
	}
	return err
}

// abort sends a best-effort close frame with the given code/reason (once)
// and shuts down the connection. Used for protocol violations detected
// while reading.
func (c *Conn) abort(code int, reason string) {
	c.closeFrameOnce.Do(func() {
		_ = c.writeControl(opClose, closePayload(code, reason))
	})
	c.shutdown()
}

// shutdown closes the underlying connection exactly once and marks the
// close as locally initiated, so a subsequent EOF isn't mistaken for an
// abnormal closure.
func (c *Conn) shutdown() {
	c.shutdownOnce.Do(func() {
		c.localClose.Store(true)
		c.conn.Close()
	})
}

func parseClosePayload(payload []byte) (int, string) {
	if len(payload) < 2 {
		return closeNoStatus, ""
	}
	code := int(binary.BigEndian.Uint16(payload[:2]))
	return code, string(payload[2:])
}

func closePayload(code int, reason string) []byte {
	buf := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(buf[:2], uint16(code))
	copy(buf[2:], reason)
	return buf
}

// writeControl writes a masked control frame, serialized against user
// writes via writeMu. The write carries a bounded deadline (see
// controlWriteTimeout) so a stalled peer can't wedge it — and, transitively,
// a Read call blocked replying to a ping — forever; the deadline is cleared
// again before returning so it doesn't leak into a subsequent WriteText/
// WriteBinary call made with a ctx that has no deadline of its own to
// overwrite it.
func (c *Conn) writeControl(opcode uint8, payload []byte) error {
	key, err := newMaskKey()
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(controlWriteTimeout))
	defer c.conn.SetWriteDeadline(time.Time{})
	return writeFrame(c.conn, true, opcode, payload, &key)
}

// newMaskKey generates a fresh 4-byte masking key using crypto/rand, as
// required for every client-to-server frame (RFC 6455 §5.3).
func newMaskKey() ([4]byte, error) {
	var key [4]byte
	if _, err := rand.Read(key[:]); err != nil {
		return key, fmt.Errorf("websocket: generate mask key: %w", err)
	}
	return key, nil
}

// WriteText sends one masked text data message. Safe for one writer
// goroutine concurrent with one reader.
func (c *Conn) WriteText(ctx context.Context, data []byte) error {
	return c.writeMessage(ctx, opText, data)
}

// WriteBinary sends one masked binary data message. Safe for one writer
// goroutine concurrent with one reader.
func (c *Conn) WriteBinary(ctx context.Context, data []byte) error {
	return c.writeMessage(ctx, opBinary, data)
}

func (c *Conn) writeMessage(ctx context.Context, opcode uint8, data []byte) error {
	return runWithContext(ctx, c.conn, dirWrite, func() error {
		key, err := newMaskKey()
		if err != nil {
			return err
		}
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		return writeFrame(c.conn, true, opcode, data, &key)
	})
}

// Close sends a close frame with the given code and reason (best effort),
// then closes the underlying connection. Idempotent, and safe to call
// concurrently with a blocked Read (which will then return an error).
// Close blocks behind an in-flight write (writeMu) — if a write may be
// stuck (e.g. a slow or unresponsive peer), use a per-call ctx deadline on
// writes rather than relying on Close to return promptly.
func (c *Conn) Close(code int, reason string) error {
	c.closeFrameOnce.Do(func() {
		_ = c.writeControl(opClose, closePayload(code, reason))
	})
	c.shutdown()
	return nil
}
