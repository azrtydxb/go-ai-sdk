package eventstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

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
