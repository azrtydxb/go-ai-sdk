package mcp

import "encoding/json"

// NotificationHandler is called for each server-initiated notification (a
// JSON-RPC message with a method but no id, e.g. "notifications/message" or
// "notifications/resources/updated"). params is the raw, undecoded params
// object from the wire; the handler is responsible for unmarshaling it into
// whatever shape the given method implies.
//
// The handler runs on a dispatch goroutine spawned by recvLoop, bounded by
// the same dispatchSem used for server-initiated requests (see
// maxConcurrentServerDispatch), and therefore may run concurrently with
// other dispatched notifications, server requests, or in-flight calls.
// Since a notification owes no reply, a handler that blocks does not delay
// any response to the server, but it does hold a dispatch slot: a handler
// that never returns will eventually starve delivery of further
// notifications and server requests once the bound is exhausted.
type NotificationHandler func(method string, params json.RawMessage)

// SetNotificationHandler installs h as the handler invoked for
// server-initiated notifications. Call this before Initialize so no
// notification sent early in the session is missed. With no handler
// installed (the default), incoming notifications are silently dropped.
//
// Delivery is best-effort: if the bounded dispatch pool (shared with
// server-initiated requests) is saturated when a notification arrives, that
// notification is dropped rather than queued or blocking recvLoop, matching
// the fire-and-forget semantics of JSON-RPC notifications.
func (c *Client) SetNotificationHandler(h NotificationHandler) {
	c.mu.Lock()
	c.notificationHandler = h
	c.mu.Unlock()
}
