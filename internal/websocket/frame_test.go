package websocket

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestMaskBytesRoundTrip(t *testing.T) {
	key := [4]byte{0x11, 0x22, 0x33, 0x44}
	original := []byte("hello, websocket world! this is a test payload.")

	data := append([]byte(nil), original...)
	maskBytes(data, key)
	if bytes.Equal(data, original) {
		t.Fatal("masking did not change the data")
	}
	maskBytes(data, key)
	if !bytes.Equal(data, original) {
		t.Fatal("masking twice with the same key did not round-trip")
	}
}

func TestWriteReadFrame_Unmasked(t *testing.T) {
	payloads := map[string][]byte{
		"empty":          {},
		"small":          []byte("hi"),
		"exactly125":     bytes.Repeat([]byte{'a'}, 125),
		"boundary126":    bytes.Repeat([]byte{'b'}, 126),
		"16bit-max":      bytes.Repeat([]byte{'c'}, 0xffff),
		"64bit-boundary": bytes.Repeat([]byte{'d'}, 0x10000),
		"64bit-larger":   bytes.Repeat([]byte{'e'}, 0x10000+37),
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeFrame(&buf, true, opBinary, payload, nil); err != nil {
				t.Fatalf("writeFrame: %v", err)
			}

			h, err := readFrameHeader(&buf)
			if err != nil {
				t.Fatalf("readFrameHeader: %v", err)
			}
			if !h.fin {
				t.Error("fin should be true")
			}
			if h.opcode != opBinary {
				t.Errorf("opcode = %d, want %d", h.opcode, opBinary)
			}
			if h.masked {
				t.Error("masked should be false")
			}
			if h.payloadLen != uint64(len(payload)) {
				t.Errorf("payloadLen = %d, want %d", h.payloadLen, len(payload))
			}

			got, err := readFramePayload(&buf, h)
			if err != nil {
				t.Fatalf("readFramePayload: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("payload mismatch: got %d bytes, want %d bytes", len(got), len(payload))
			}
		})
	}
}

func TestWriteReadFrame_Masked(t *testing.T) {
	payload := []byte("masked client frame payload")
	key := [4]byte{0xde, 0xad, 0xbe, 0xef}

	var buf bytes.Buffer
	if err := writeFrame(&buf, true, opText, payload, &key); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	// The wire bytes must differ from the plaintext payload (i.e. it really
	// got masked), and writeFrame must not have mutated the caller's slice.
	wire := buf.Bytes()
	if bytes.Contains(wire, payload) {
		t.Error("masked payload appears unmasked on the wire")
	}
	want := []byte("masked client frame payload")
	if !bytes.Equal(payload, want) {
		t.Fatal("writeFrame mutated the caller's payload slice")
	}

	h, err := readFrameHeader(&buf)
	if err != nil {
		t.Fatalf("readFrameHeader: %v", err)
	}
	if !h.masked {
		t.Fatal("masked should be true")
	}
	if h.maskKey != key {
		t.Errorf("maskKey = %v, want %v", h.maskKey, key)
	}

	got, err := readFramePayload(&buf, h)
	if err != nil {
		t.Fatalf("readFramePayload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("unmasked payload = %q, want %q", got, payload)
	}
}

func TestReadFrameHeader_ControlFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	oversized := bytes.Repeat([]byte{'x'}, 126)
	if err := writeRawControlFrame(&buf, opPing, oversized); err != nil {
		t.Fatalf("writeRawControlFrame: %v", err)
	}

	_, err := readFrameHeader(&buf)
	var perr protocolErr
	if !errors.As(err, &perr) {
		t.Fatalf("readFrameHeader err = %v, want protocolErr", err)
	}
}

func TestReadFrameHeader_ControlFrameFragmented(t *testing.T) {
	var buf bytes.Buffer
	// fin=false control frame is a direct protocol violation regardless of
	// payload size.
	if err := writeFrame(&buf, false, opPing, []byte("hi"), nil); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}

	_, err := readFrameHeader(&buf)
	var perr protocolErr
	if !errors.As(err, &perr) {
		t.Fatalf("readFrameHeader err = %v, want protocolErr", err)
	}
}

func TestReadFrameHeader_TruncatedHeader(t *testing.T) {
	_, err := readFrameHeader(bytes.NewReader(nil))
	if err == nil || !errors.Is(err, io.EOF) {
		t.Fatalf("readFrameHeader err = %v, want io.EOF", err)
	}
}

// writeRawControlFrame writes a control frame header directly, bypassing
// writeFrame's own validation, so tests can construct wire-level violations.
func writeRawControlFrame(w io.Writer, opcode uint8, payload []byte) error {
	var header [10]byte
	n := 0
	header[0] = 0x80 | (opcode & 0x0f)
	n++
	switch {
	case len(payload) <= 125:
		header[1] = byte(len(payload))
		n++
	case len(payload) <= 0xffff:
		header[1] = 126
		n++
		header[2] = byte(len(payload) >> 8)
		header[3] = byte(len(payload))
		n += 2
	}
	if _, err := w.Write(header[:n]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
