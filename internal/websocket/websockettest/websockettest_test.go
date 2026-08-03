package websockettest

import (
	"net"
	"testing"
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
