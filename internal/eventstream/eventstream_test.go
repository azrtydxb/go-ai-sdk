package eventstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

// rawHeader is a single (name, type, encoded value bytes) header tuple used
// by buildFrame to construct frames containing non-string header types,
// which the public Encode function (string headers only) cannot produce.
type rawHeader struct {
	name    string
	valType byte
	value   []byte // pre-encoded value bytes (not including any length prefix)
}

// buildFrame constructs a raw event stream frame directly from a set of
// typed headers and a payload, mirroring Encode's framing but allowing
// header value types other than 7 (string). Headers with valType 6 or 7 get
// a 2-byte big-endian length prefix written automatically; other types are
// written as fixed-size payloads with no prefix, per the AWS event stream
// header encoding.
func buildFrame(t *testing.T, headers []rawHeader, payload []byte) []byte {
	t.Helper()
	var headerBuf bytes.Buffer
	for _, h := range headers {
		headerBuf.WriteByte(byte(len(h.name)))
		headerBuf.WriteString(h.name)
		headerBuf.WriteByte(h.valType)
		switch h.valType {
		case headerTypeStr, headerTypeByteArray:
			var lenBuf [2]byte
			binary.BigEndian.PutUint16(lenBuf[:], uint16(len(h.value)))
			headerBuf.Write(lenBuf[:])
			headerBuf.Write(h.value)
		default:
			headerBuf.Write(h.value)
		}
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

func TestRoundTrip_SingleMessage(t *testing.T) {
	headers := map[string]string{
		":message-type": "event",
		":event-type":   "contentBlockDelta",
	}
	payload := []byte(`{"delta":{"text":"hi"}}`)

	frame := Encode(headers, payload)

	var got []Message
	for msg, err := range Scan(bytes.NewReader(frame)) {
		if err != nil {
			t.Fatalf("Scan: unexpected error: %v", err)
		}
		got = append(got, msg)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Headers[":event-type"] != "contentBlockDelta" {
		t.Errorf("headers = %+v, want :event-type=contentBlockDelta", got[0].Headers)
	}
	if got[0].Headers[":message-type"] != "event" {
		t.Errorf("headers = %+v, want :message-type=event", got[0].Headers)
	}
	if !bytes.Equal(got[0].Payload, payload) {
		t.Errorf("Payload = %q, want %q", got[0].Payload, payload)
	}
}

func TestRoundTrip_MultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(Encode(map[string]string{":event-type": "messageStart"}, []byte(`{"role":"assistant"}`)))
	buf.Write(Encode(map[string]string{":event-type": "contentBlockDelta"}, []byte(`{"delta":{"text":"a"}}`)))
	buf.Write(Encode(map[string]string{":event-type": "contentBlockDelta"}, []byte(`{"delta":{"text":"b"}}`)))
	buf.Write(Encode(map[string]string{":event-type": "messageStop"}, []byte(`{"stopReason":"end_turn"}`)))

	var events []string
	for msg, err := range Scan(&buf) {
		if err != nil {
			t.Fatalf("Scan: unexpected error: %v", err)
		}
		events = append(events, msg.Headers[":event-type"])
	}
	want := []string{"messageStart", "contentBlockDelta", "contentBlockDelta", "messageStop"}
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(events), len(want), events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestScan_EmptyStream(t *testing.T) {
	var count int
	for _, err := range Scan(bytes.NewReader(nil)) {
		if err != nil {
			t.Fatalf("Scan: unexpected error on empty stream: %v", err)
		}
		count++
	}
	if count != 0 {
		t.Errorf("got %d messages from empty stream, want 0", count)
	}
}

func TestScan_CorruptedPreludeCRC(t *testing.T) {
	frame := Encode(map[string]string{":event-type": "x"}, []byte("payload"))
	// Corrupt a byte within the prelude CRC field (bytes 8-11).
	frame[9] ^= 0xFF

	var gotErr error
	for _, err := range Scan(bytes.NewReader(frame)) {
		gotErr = err
		break
	}
	if gotErr == nil {
		t.Fatal("Scan: want error for corrupted prelude CRC, got nil")
	}
	if !errors.Is(gotErr, ErrInvalidCRC) {
		t.Errorf("Scan error = %v, want wrapping ErrInvalidCRC", gotErr)
	}
}

func TestScan_CorruptedMessageCRC(t *testing.T) {
	frame := Encode(map[string]string{":event-type": "x"}, []byte("payload"))
	// Corrupt a payload byte, which invalidates the message CRC but leaves
	// the prelude CRC intact.
	payloadStart := len(frame) - 4 /*message crc*/ - len("payload")
	frame[payloadStart] ^= 0xFF

	var gotErr error
	for _, err := range Scan(bytes.NewReader(frame)) {
		gotErr = err
		break
	}
	if gotErr == nil {
		t.Fatal("Scan: want error for corrupted message CRC, got nil")
	}
	if !errors.Is(gotErr, ErrInvalidCRC) {
		t.Errorf("Scan error = %v, want wrapping ErrInvalidCRC", gotErr)
	}
}

func TestScan_TruncatedFrame(t *testing.T) {
	frame := Encode(map[string]string{":event-type": "x"}, []byte("payload"))
	truncated := frame[:len(frame)-5] // cut off mid-payload/CRC

	var gotErr error
	var gotMsg bool
	for msg, err := range Scan(bytes.NewReader(truncated)) {
		if err != nil {
			gotErr = err
			break
		}
		_ = msg
		gotMsg = true
	}
	if gotErr == nil {
		t.Fatal("Scan: want error for truncated frame, got nil")
	}
	if gotMsg {
		t.Error("Scan: yielded a Message for a truncated frame, want none")
	}
}

func TestScan_TruncatedPrelude(t *testing.T) {
	frame := Encode(map[string]string{":event-type": "x"}, []byte("payload"))
	truncated := frame[:5] // less than the 12-byte prelude+CRC

	var gotErr error
	for _, err := range Scan(bytes.NewReader(truncated)) {
		gotErr = err
		break
	}
	if gotErr == nil {
		t.Fatal("Scan: want error for truncated prelude, got nil")
	}
}

// TestScan_SkipsNonStringHeaderWithoutDesync covers the fix for parseHeaders
// treating non-string header types as a hard error: a well-formed
// int32-typed header must be silently skipped (not surfaced in
// Message.Headers, no error), and the string header that follows it in the
// same frame must still decode correctly, AND a subsequent frame in the
// same stream must still parse cleanly (proving the int32's 4-byte payload
// was consumed correctly rather than desyncing the header cursor).
func TestScan_SkipsNonStringHeaderWithoutDesync(t *testing.T) {
	frame1 := buildFrame(t, []rawHeader{
		{name: "count", valType: headerTypeInt32, value: []byte{0x00, 0x00, 0x00, 0x2A}}, // int32 = 42
		{name: ":event-type", valType: headerTypeStr, value: []byte("contentBlockDelta")},
	}, []byte(`{"delta":{"text":"a"}}`))

	frame2 := Encode(map[string]string{":event-type": "messageStop"}, []byte(`{"stopReason":"end_turn"}`))

	var buf bytes.Buffer
	buf.Write(frame1)
	buf.Write(frame2)

	var got []Message
	for msg, err := range Scan(&buf) {
		if err != nil {
			t.Fatalf("Scan: unexpected error: %v", err)
		}
		got = append(got, msg)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (no desync)", len(got))
	}

	if _, present := got[0].Headers["count"]; present {
		t.Errorf("Headers contains skipped int32 header %q, want it absent", "count")
	}
	if got[0].Headers[":event-type"] != "contentBlockDelta" {
		t.Errorf("frame1 :event-type = %q, want contentBlockDelta", got[0].Headers[":event-type"])
	}
	if string(got[0].Payload) != `{"delta":{"text":"a"}}` {
		t.Errorf("frame1 Payload = %q, want the original JSON payload", got[0].Payload)
	}

	if got[1].Headers[":event-type"] != "messageStop" {
		t.Errorf("frame2 :event-type = %q, want messageStop (proves no desync from frame1)", got[1].Headers[":event-type"])
	}
}

func TestEncode_FrameStructureSanity(t *testing.T) {
	frame := Encode(map[string]string{"a": "1"}, []byte("xy"))
	if len(frame) < 12 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	totalLen := binary.BigEndian.Uint32(frame[0:4])
	if int(totalLen) != len(frame) {
		t.Errorf("encoded total length = %d, actual frame length = %d", totalLen, len(frame))
	}
}
