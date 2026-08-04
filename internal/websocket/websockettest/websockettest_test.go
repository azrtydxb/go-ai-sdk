package websockettest

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// TestReadMessage_DropsCachedReaderOnEOF pins the fix for a cheap-hardening
// finding: connReaders must not keep a *bufio.Reader alive forever for a
// conn that has already reached EOF.
func TestReadMessage_DropsCachedReaderOnEOF(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	go func() {
		client.Close() // no data ever written; the server side sees EOF
	}()

	if _, _, err := ReadMessage(server); err == nil {
		t.Fatal("expected an error (EOF) reading from a closed pipe")
	}

	if _, ok := connReaders.Load(server); ok {
		t.Error("expected the cached bufio.Reader to be dropped after EOF")
	}
}

// TestReadMessage_RejectsOversizedDeclaredLength pins that ReadMessageBuf
// rejects a frame whose declared (extended, 8-byte) length exceeds
// maxFrameLength before allocating a buffer for it, so a misbehaving client
// fixture can't OOM the test server by declaring an enormous frame length.
func TestReadMessage_RejectsOversizedDeclaredLength(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		// fin=1, opcode=text (0x81); mask bit set + length=127 (extended
		// 8-byte length follows) = 0xFF.
		header := []byte{0x81, 0xFF}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(maxFrameLength)+1)
		client.Write(header)
		client.Write(ext[:])
		// The declared length must be rejected before ReadMessageBuf tries
		// to read a mask key or payload, so no further bytes are written
		// (and none are needed for the read to return).
	}()

	done := make(chan struct{})
	var err error
	go func() {
		_, _, err = ReadMessage(server)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadMessage did not return promptly for an oversized declared length")
	}
	if err == nil {
		t.Fatal("want error for declared length exceeding cap, got nil")
	}
}
