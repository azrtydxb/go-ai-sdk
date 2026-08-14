// Package eventstreamtest builds AWS event stream frames for test
// fixtures. The production eventstream package is decode-only; encoding is
// needed only by its own tests and by providers/bedrock's test fixtures.
package eventstreamtest

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"slices"
)

const (
	preludeLen    = 8 // total length (4) + headers length (4)
	crcLen        = 4
	headerTypeStr = 7
)

// Encode builds a single event stream frame with the given headers and
// payload. All header values are encoded as type 7 (string); headers are
// written in sorted-by-name order so output is deterministic.
func Encode(headers map[string]string, payload []byte) []byte {
	names := make([]string, 0, len(headers))
	for n := range headers {
		names = append(names, n)
	}
	slices.Sort(names)

	var headerBuf bytes.Buffer
	for _, name := range names {
		value := headers[name]
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
