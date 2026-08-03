// Package websockettest provides minimal RFC 6455 server-side helpers for
// testing internal/websocket's client, and for fixture servers used by
// provider packages elsewhere in this module. It performs the server side
// of the opening handshake and reads/writes unmasked frames; it is not a
// general-purpose WebSocket server implementation (no extensions, no
// subprotocol negotiation, no fragmentation reassembly on read — callers
// that need to observe multi-frame client messages should read frame by
// frame with ReadMessage, which returns exactly one frame's opcode and
// payload).
//
// Every function here takes and returns plain values (no *testing.T), so
// it can be used from non-_test.go code such as provider fixture servers.
package websockettest

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const wsMagic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcodes, RFC 6455 §11.8.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xa
)

// Accept accepts one connection from l and performs the server side of the
// RFC 6455 opening handshake on it. On any failure the accepted connection
// (if any) is closed and an error is returned.
func Accept(l net.Listener) (net.Conn, error) {
	conn, err := l.Accept()
	if err != nil {
		return nil, err
	}
	if err := Upgrade(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Upgrade reads an HTTP upgrade request off conn and writes the RFC 6455
// 101 response, computing Sec-WebSocket-Accept from the client's
// Sec-WebSocket-Key. It does not consume more of conn than the request
// headers.
func Upgrade(conn net.Conn) error {
	br := bufReaderFor(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return fmt.Errorf("websockettest: read request: %w", err)
	}
	defer req.Body.Close()

	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return fmt.Errorf("websockettest: missing Upgrade: websocket header")
	}
	key := req.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return fmt.Errorf("websockettest: missing Sec-WebSocket-Key header")
	}

	accept := computeAccept(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		return fmt.Errorf("websockettest: write response: %w", err)
	}
	return nil
}

func computeAccept(key string) string {
	return ComputeAccept(key)
}

// ComputeAccept derives the Sec-WebSocket-Accept value for a given
// Sec-WebSocket-Key, per RFC 6455 §1.3. Exported for callers that hijack
// an HTTP connection themselves (e.g. via httptest.NewTLSServer) instead
// of using Accept/Upgrade.
func ComputeAccept(key string) string {
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(wsMagic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// connReaders caches one *bufio.Reader per net.Conn so repeated ReadMessage
// (and the internal Upgrade) calls against the same connection share
// buffered input instead of each discarding whatever the previous
// bufio.Reader had already pulled off the socket.
var connReaders sync.Map // net.Conn -> *bufio.Reader

func bufReaderFor(conn net.Conn) *bufio.Reader {
	if v, ok := connReaders.Load(conn); ok {
		return v.(*bufio.Reader)
	}
	br := bufio.NewReader(conn)
	actual, _ := connReaders.LoadOrStore(conn, br)
	return actual.(*bufio.Reader)
}

// ReadMessage reads exactly one frame from conn (server-side reads: the
// client is required to mask, so this verifies the mask bit is set and
// unmasks the payload). It returns the frame's opcode and payload as-is;
// callers that expect a fragmented message must call ReadMessage
// repeatedly and reassemble continuation frames themselves. Successive
// calls for the same conn share a buffered reader, so bytes read ahead by
// one call are visible to the next. Once conn reaches EOF its cached
// reader is dropped, so a conn value that's later reused (e.g. a reused
// *net.TCPConn wrapper, or simply to bound the cache's lifetime) doesn't
// keep a stale, exhausted *bufio.Reader alive forever.
func ReadMessage(conn net.Conn) (opcode int, payload []byte, err error) {
	opcode, payload, err = ReadMessageBuf(bufReaderFor(conn))
	if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
		connReaders.Delete(conn)
	}
	return opcode, payload, err
}

// ReadMessageBuf is like ReadMessage but reads from an existing
// *bufio.Reader wrapping conn, so callers that need to issue several reads
// against the same connection don't lose buffered bytes between calls.
func ReadMessageBuf(br *bufio.Reader) (opcode int, payload []byte, err error) {
	var lead [2]byte
	if _, err := io.ReadFull(br, lead[:]); err != nil {
		return 0, nil, err
	}
	fin := lead[0]&0x80 != 0
	_ = fin
	op := int(lead[0] & 0x0f)
	masked := lead[1]&0x80 != 0
	if !masked {
		return 0, nil, fmt.Errorf("websockettest: client frame missing mask bit")
	}

	length := uint64(lead[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	var maskKey [4]byte
	if _, err := io.ReadFull(br, maskKey[:]); err != nil {
		return 0, nil, err
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(br, data); err != nil {
		return 0, nil, err
	}
	for i := range data {
		data[i] ^= maskKey[i%4]
	}

	return op, data, nil
}

// WriteMessage writes one unmasked, unfragmented frame (fin=true) with the
// given opcode and payload — the server role never masks.
func WriteMessage(conn net.Conn, opcode int, payload []byte) error {
	return WriteFrame(conn, true, opcode, payload)
}

// WriteFragmented writes payload split across len(parts) frames: the first
// carries opcode, subsequent ones carry the continuation opcode, and only
// the last has fin=true. Passing zero parts is a no-op.
func WriteFragmented(conn net.Conn, opcode int, parts ...[]byte) error {
	for i, part := range parts {
		op := OpContinuation
		if i == 0 {
			op = opcode
		}
		fin := i == len(parts)-1
		if err := WriteFrame(conn, fin, op, part); err != nil {
			return err
		}
	}
	return nil
}

// WriteFrame writes a single raw, unmasked frame with the given fin bit,
// opcode, and payload. Exposed so callers can build custom frame sequences
// (e.g. a control frame interleaved between fragments).
func WriteFrame(conn net.Conn, fin bool, opcode int, payload []byte) error {
	var header bytes.Buffer

	var first byte
	if fin {
		first |= 0x80
	}
	first |= byte(opcode) & 0x0f
	header.WriteByte(first)

	switch {
	case len(payload) <= 125:
		header.WriteByte(byte(len(payload)))
	case len(payload) <= 0xffff:
		header.WriteByte(126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		header.Write(ext[:])
	default:
		header.WriteByte(127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		header.Write(ext[:])
	}

	if _, err := conn.Write(header.Bytes()); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

// WriteClose writes an unmasked close frame with the given status code and
// reason.
func WriteClose(conn net.Conn, code int, reason string) error {
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload[:2], uint16(code))
	copy(payload[2:], reason)
	return WriteFrame(conn, true, OpClose, payload)
}
