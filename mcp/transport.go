package mcp

import (
	"context"
	"encoding/json"
)

// Transport moves one JSON-RPC message each way. Implementations are safe
// for one concurrent reader and one concurrent writer (i.e. Send may be
// called concurrently with Receive, but Send is expected to be called by at
// most one goroutine at a time, as is Receive).
type Transport interface {
	Send(ctx context.Context, msg json.RawMessage) error
	Receive(ctx context.Context) (json.RawMessage, error)
	Close() error
}
