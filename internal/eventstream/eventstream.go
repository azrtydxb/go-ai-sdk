// Package eventstream implements a minimal reader/writer for the AWS binary
// event stream format (content-type application/vnd.amazon.eventstream)
// used by streaming AWS APIs such as Bedrock's ConverseStream.
//
// Frame layout (all integers big-endian):
//
//	[4B total length][4B headers length][4B prelude CRC32][headers][payload][4B message CRC32]
//
// Header layout (repeated to fill the headers section):
//
//	[1B name length][name][1B value type][2B value length][value]
//
// Only string-typed header values (type 7) are decoded; other header value
// types are skipped. CRC32 uses the IEEE polynomial.
package eventstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"iter"
)

// Message is one decoded event stream frame.
type Message struct {
	Headers map[string]string
	Payload []byte
}

const (
	preludeLen    = 8 // total length (4) + headers length (4)
	crcLen        = 4
	headerTypeStr = 7
)

// ErrInvalidCRC is returned (wrapped) when a frame's prelude or message CRC
// does not match its computed value.
var ErrInvalidCRC = errors.New("eventstream: invalid CRC32")

// Scan parses a stream of event stream frames from r, yielding one Message
// per frame. An invalid CRC or a truncated frame yields the error and stops
// (no partial Message is yielded for that frame).
func Scan(r io.Reader) iter.Seq2[Message, error] {
	return func(yield func(Message, error) bool) {
		for {
			msg, err := readMessage(r)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				yield(Message{}, err)
				return
			}
			if !yield(msg, nil) {
				return
			}
		}
	}
}

func readMessage(r io.Reader) (Message, error) {
	prelude := make([]byte, preludeLen+crcLen)
	if _, err := io.ReadFull(r, prelude); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Message{}, fmt.Errorf("eventstream: truncated frame prelude: %w", err)
		}
		return Message{}, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if got := crc32.ChecksumIEEE(prelude[0:8]); got != preludeCRC {
		return Message{}, fmt.Errorf("eventstream: %w: prelude CRC mismatch (got %#x, want %#x)", ErrInvalidCRC, got, preludeCRC)
	}

	if totalLen < preludeLen+crcLen+crcLen {
		return Message{}, fmt.Errorf("eventstream: invalid total length %d", totalLen)
	}

	// Remaining bytes after the prelude+preludeCRC: headers + payload +
	// trailing message CRC.
	remaining := int(totalLen) - (preludeLen + crcLen)
	rest := make([]byte, remaining)
	if _, err := io.ReadFull(r, rest); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Message{}, fmt.Errorf("eventstream: truncated frame body: %w", err)
		}
		return Message{}, err
	}

	if int(headersLen) > remaining-crcLen {
		return Message{}, fmt.Errorf("eventstream: invalid headers length %d", headersLen)
	}

	headerBytes := rest[:headersLen]
	payload := rest[headersLen : remaining-crcLen]
	messageCRC := binary.BigEndian.Uint32(rest[remaining-crcLen:])

	full := make([]byte, 0, len(prelude)+len(rest))
	full = append(full, prelude...)
	full = append(full, rest...)
	got := crc32.ChecksumIEEE(full[:len(full)-crcLen])
	if got != messageCRC {
		return Message{}, fmt.Errorf("eventstream: %w: message CRC mismatch (got %#x, want %#x)", ErrInvalidCRC, got, messageCRC)
	}

	headers, err := parseHeaders(headerBytes)
	if err != nil {
		return Message{}, err
	}

	return Message{Headers: headers, Payload: payload}, nil
}

func parseHeaders(b []byte) (map[string]string, error) {
	headers := map[string]string{}
	for len(b) > 0 {
		if len(b) < 1 {
			return nil, fmt.Errorf("eventstream: truncated header name length")
		}
		nameLen := int(b[0])
		b = b[1:]
		if len(b) < nameLen+1 {
			return nil, fmt.Errorf("eventstream: truncated header name/type")
		}
		name := string(b[:nameLen])
		b = b[nameLen:]
		valType := b[0]
		b = b[1:]

		switch valType {
		case headerTypeStr:
			if len(b) < 2 {
				return nil, fmt.Errorf("eventstream: truncated header value length")
			}
			valLen := int(binary.BigEndian.Uint16(b[:2]))
			b = b[2:]
			if len(b) < valLen {
				return nil, fmt.Errorf("eventstream: truncated header value")
			}
			headers[name] = string(b[:valLen])
			b = b[valLen:]
		default:
			// Skip non-string header values; we don't know their encoded
			// length ahead of time for most types, so treat any header
			// stream containing them as unsupported for this minimal
			// reader.
			return nil, fmt.Errorf("eventstream: unsupported header value type %d for header %q", valType, name)
		}
	}
	return headers, nil
}

// Encode builds a single event stream frame with the given headers and
// payload. All header values are encoded as type 7 (string).
func Encode(headers map[string]string, payload []byte) []byte {
	var headerBuf bytes.Buffer
	for name, value := range sortedHeaders(headers) {
		headerBuf.WriteByte(byte(len(name)))
		headerBuf.WriteString(name)
		headerBuf.WriteByte(headerTypeStr)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(value)))
		headerBuf.Write(lenBuf[:])
		headerBuf.WriteString(value)
	}
	headerBytes := headerBuf.Bytes()

	totalLen := preludeLen + crcLen + len(headerBytes) + len(payload) + crcLen

	buf := make([]byte, 0, totalLen)
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(totalLen))
	buf = append(buf, tmp[:]...)
	binary.BigEndian.PutUint32(tmp[:], uint32(len(headerBytes)))
	buf = append(buf, tmp[:]...)

	preludeCRC := crc32.ChecksumIEEE(buf)
	binary.BigEndian.PutUint32(tmp[:], preludeCRC)
	buf = append(buf, tmp[:]...)

	buf = append(buf, headerBytes...)
	buf = append(buf, payload...)

	messageCRC := crc32.ChecksumIEEE(buf)
	binary.BigEndian.PutUint32(tmp[:], messageCRC)
	buf = append(buf, tmp[:]...)

	return buf
}

// sortedHeaders returns headers as a slice of name/value pairs in a stable
// (sorted by name) order, so Encode output is deterministic.
func sortedHeaders(headers map[string]string) iter.Seq2[string, string] {
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	// simple insertion sort; header counts are tiny (<10)
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return func(yield func(string, string) bool) {
		for _, n := range names {
			if !yield(n, headers[n]) {
				return
			}
		}
	}
}
