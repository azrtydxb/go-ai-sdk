package mcp

import (
	"context"
	"encoding/json"
)

// Transport moves one JSON-RPC message each way. Implementations are safe
// for one concurrent reader and one concurrent writer (i.e. Send may be
// called concurrently with Receive, but Send is expected to be called by at
// most one goroutine at a time, as is Receive) — UNLESS the implementation
// also implements selfSerializingTransport and reports SelfSerializes() ==
// true, in which case concurrent Send calls from multiple goroutines are
// required to be safe (see that interface's doc).
type Transport interface {
	Send(ctx context.Context, msg json.RawMessage) error
	Receive(ctx context.Context) (json.RawMessage, error)
	Close() error
}

// selfSerializingTransport is an optional capability a Transport may
// implement to declare that it does not need Client's write serialization
// (sendMu/sendSem): concurrent Send calls from multiple goroutines are
// already safe on it.
//
// Client checks for this once, at NewClient, and stores the result — every
// Send site (call, notify, sendServerResponse) is gated on that stored bool
// rather than a per-call type assertion.
//
// httpTransport implements this and returns true: each Send is an
// independent POST (a fresh *http.Request built from immutable/mu-guarded
// state, dispatched via http.Client.Do which is documented safe for
// concurrent use) and responses correlate by JSON-RPC id in recvLoop, not
// by ordering — see http.go's SelfSerializes doc for the full analysis.
//
// The stdio framedTransport does NOT implement this: newline-delimited
// framing requires that one Send's bytes (including its trailing newline)
// reach the wire before another Send's bytes start, so interleaved
// concurrent writes would corrupt framing. It needs external serialization
// (sendSem) exactly as before.
type selfSerializingTransport interface {
	// SelfSerializes reports whether concurrent Send calls are safe without
	// external serialization.
	SelfSerializes() bool
}
