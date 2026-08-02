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
//	[1B name length][name][1B value type][value]
//
// The value's own encoding depends on its type: only byte-array (type 6)
// and string (type 7) are length-prefixed ([2B value length][value]); the
// other types have a fixed, type-implied size (0 bytes for the two boolean
// types, 1/2/4/8/8/16 bytes for byte/int16/int32/int64/timestamp/uuid) and
// carry no length prefix on the wire.
//
// Only string-typed header values (type 7) are decoded into Message.Headers.
// Other header value types (0=bool-true, 1=bool-false, 2=byte, 3=int16,
// 4=int32, 5=int64, 6=byte-array, 8=timestamp, 9=uuid) are recognized,
// their payload bytes are read and discarded (skipped) using each type's
// known or length-prefixed size, and parsing continues with the next
// header — they do not appear in Message.Headers and do not desync the
// parse of subsequent headers or frames. CRC32 uses the IEEE polynomial.
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

	// maxTotalLen is a sanity upper bound on a frame's declared total
	// length: no legitimate Bedrock streaming event approaches this size,
	// so a prelude claiming more is either corrupted or malicious, and must
	// be rejected before attempting to read (and allocate a buffer for)
	// that many bytes.
	maxTotalLen = 16 * 1024 * 1024
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
	if totalLen > maxTotalLen {
		return Message{}, fmt.Errorf("eventstream: total length %d exceeds sanity ceiling %d", totalLen, maxTotalLen)
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

// Header value types, per the AWS event stream spec. Only type 7 (string)
// is decoded into Message.Headers; the others are recognized only so their
// payload length can be computed and skipped without desyncing the parse
// of subsequent headers/frames.
const (
	headerTypeBoolTrue  = 0 // no payload
	headerTypeBoolFalse = 1 // no payload
	headerTypeByte      = 2 // 1 byte
	headerTypeInt16     = 3 // 2 bytes
	headerTypeInt32     = 4 // 4 bytes
	headerTypeInt64     = 5 // 8 bytes
	headerTypeByteArray = 6 // 2-byte length prefix
	// headerTypeStr = 7 (declared above; 2-byte length prefix)
	headerTypeTimestamp = 8 // 8 bytes
	headerTypeUUID      = 9 // 16 bytes
)

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
		case headerTypeStr, headerTypeByteArray:
			// Both string and byte-array values share a 2-byte length
			// prefix; only string (7) is surfaced in Message.Headers, but
			// byte-array (6) must still be length-decoded and skipped so
			// the parse doesn't desync.
			if len(b) < 2 {
				return nil, fmt.Errorf("eventstream: truncated header value length")
			}
			valLen := int(binary.BigEndian.Uint16(b[:2]))
			b = b[2:]
			if len(b) < valLen {
				return nil, fmt.Errorf("eventstream: truncated header value")
			}
			if valType == headerTypeStr {
				headers[name] = string(b[:valLen])
			}
			b = b[valLen:]

		case headerTypeBoolTrue, headerTypeBoolFalse:
			// No payload bytes to skip.

		case headerTypeByte:
			if len(b) < 1 {
				return nil, fmt.Errorf("eventstream: truncated header value (byte)")
			}
			b = b[1:]

		case headerTypeInt16:
			if len(b) < 2 {
				return nil, fmt.Errorf("eventstream: truncated header value (int16)")
			}
			b = b[2:]

		case headerTypeInt32:
			if len(b) < 4 {
				return nil, fmt.Errorf("eventstream: truncated header value (int32)")
			}
			b = b[4:]

		case headerTypeInt64, headerTypeTimestamp:
			if len(b) < 8 {
				return nil, fmt.Errorf("eventstream: truncated header value (int64/timestamp)")
			}
			b = b[8:]

		case headerTypeUUID:
			if len(b) < 16 {
				return nil, fmt.Errorf("eventstream: truncated header value (uuid)")
			}
			b = b[16:]

		default:
			return nil, fmt.Errorf("eventstream: unknown header value type %d for header %q", valType, name)
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
