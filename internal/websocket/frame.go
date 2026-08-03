package websocket

import (
	"encoding/binary"
	"io"
)

// WebSocket opcodes, RFC 6455 §11.8.
const (
	opContinuation uint8 = 0x0
	opText         uint8 = 0x1
	opBinary       uint8 = 0x2
	opClose        uint8 = 0x8
	opPing         uint8 = 0x9
	opPong         uint8 = 0xa
)

// maxControlFramePayload is the RFC 6455 §5.5 limit on control frame
// payload size.
const maxControlFramePayload = 125

// protocolErr is a low-level framing violation. The connection layer maps
// it to a close with CloseProtocolError (1002).
type protocolErr string

func (e protocolErr) Error() string { return string(e) }

func isControlOpcode(opcode uint8) bool {
	return opcode&0x8 != 0
}

// frameHeader is a parsed RFC 6455 §5.2 frame header, excluding the
// payload itself.
type frameHeader struct {
	fin        bool
	opcode     uint8
	masked     bool
	maskKey    [4]byte
	payloadLen uint64
}

// readFrameHeader reads and parses one frame header, including the extended
// length fields and mask key (if present), from r. It does not read the
// payload. Control-frame constraints (payload <=125 bytes, never
// fragmented) are validated here since they depend only on the header.
func readFrameHeader(r io.Reader) (frameHeader, error) {
	var lead [2]byte
	if _, err := io.ReadFull(r, lead[:]); err != nil {
		return frameHeader{}, err
	}

	h := frameHeader{
		fin:    lead[0]&0x80 != 0,
		opcode: lead[0] & 0x0f,
		masked: lead[1]&0x80 != 0,
	}
	// Reserved bits (lead[0] & 0x70) are ignored: no extensions are
	// negotiated by this client, so RSV1-3 should always be zero, and we
	// don't police that against a misbehaving server here.

	length := uint64(lead[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frameHeader{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frameHeader{}, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	h.payloadLen = length

	if h.masked {
		if _, err := io.ReadFull(r, h.maskKey[:]); err != nil {
			return frameHeader{}, err
		}
	}

	if isControlOpcode(h.opcode) {
		if h.payloadLen > maxControlFramePayload {
			return frameHeader{}, protocolErr("websocket: control frame payload exceeds 125 bytes")
		}
		if !h.fin {
			return frameHeader{}, protocolErr("websocket: control frame must not be fragmented")
		}
	}

	return h, nil
}

// readFramePayload reads exactly h.payloadLen bytes from r and unmasks them
// in place if the frame was masked.
func readFramePayload(r io.Reader, h frameHeader) ([]byte, error) {
	payload := make([]byte, h.payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if h.masked {
		maskBytes(payload, h.maskKey)
	}
	return payload, nil
}

// maskBytes applies (or removes: XOR is its own inverse) a WebSocket
// masking key to data in place, per RFC 6455 §5.3.
func maskBytes(data []byte, key [4]byte) {
	for i := range data {
		data[i] ^= key[i%4]
	}
}

// writeFrame writes one complete frame to w. If maskKey is non-nil the
// payload is masked with that key before being written (the client role);
// a nil maskKey writes an unmasked frame (the server role, used only by
// test helpers). payload is never mutated.
func writeFrame(w io.Writer, fin bool, opcode uint8, payload []byte, maskKey *[4]byte) error {
	var header [14]byte
	n := 0

	var first byte
	if fin {
		first |= 0x80
	}
	first |= opcode & 0x0f
	header[0] = first
	n++

	var second byte
	if maskKey != nil {
		second |= 0x80
	}

	switch {
	case len(payload) <= 125:
		second |= byte(len(payload))
		header[1] = second
		n++
	case len(payload) <= 0xffff:
		second |= 126
		header[1] = second
		binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)))
		n += 1 + 2
	default:
		second |= 127
		header[1] = second
		binary.BigEndian.PutUint64(header[2:10], uint64(len(payload)))
		n += 1 + 8
	}

	if maskKey != nil {
		copy(header[n:n+4], maskKey[:])
		n += 4
	}

	if _, err := w.Write(header[:n]); err != nil {
		return err
	}

	if len(payload) == 0 {
		return nil
	}

	if maskKey == nil {
		_, err := w.Write(payload)
		return err
	}

	masked := make([]byte, len(payload))
	copy(masked, payload)
	maskBytes(masked, *maskKey)
	_, err := w.Write(masked)
	return err
}
