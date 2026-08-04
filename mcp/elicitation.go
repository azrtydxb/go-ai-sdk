package mcp

import (
	"context"
	"encoding/json"
)

// ElicitationRequest is the payload of a server-initiated "elicitation/create"
// request: the server is asking the client to gather structured input from
// the user mid-session.
type ElicitationRequest struct {
	Message         string
	RequestedSchema json.RawMessage // JSON schema of the requested object
}

// ElicitationResult is the client's reply to an ElicitationRequest.
type ElicitationResult struct {
	Action  string         // "accept" | "decline" | "cancel"
	Content map[string]any // set when Action == "accept"
}

// ElicitationHandler is called when the server sends an "elicitation/create"
// request. A nil handler installed on the Client causes it to auto-respond
// with Action "decline" and not declare the "elicitation" capability during
// Initialize.
type ElicitationHandler func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error)

// SetElicitationHandler installs h as the handler invoked for
// "elicitation/create" requests from the server. Call this before
// Initialize: the "elicitation" capability is only declared to the server
// when a handler has been set.
func (c *Client) SetElicitationHandler(h ElicitationHandler) {
	c.mu.Lock()
	c.elicitationHandler = h
	c.mu.Unlock()
}

// elicitationCreateParamsWire is the wire shape of "elicitation/create"
// request params.
type elicitationCreateParamsWire struct {
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
}

// elicitationCreateResultWire is the wire shape of the client's reply to
// "elicitation/create".
type elicitationCreateResultWire struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content,omitempty"`
}

// -32601 is the standard JSON-RPC "Method not found" error code.
const rpcMethodNotFound = -32601

// -32602 is the standard JSON-RPC "Invalid params" error code.
const rpcInvalidParams = -32602

// dispatchServerRequest handles one server-initiated request (recognized by
// having both an id and a method) and sends the matching response back to
// the server. It runs on its own goroutine (spawned by recvLoop) so a slow
// handler cannot block further reads; the response write itself goes
// through sendMu like any other transport write.
func (c *Client) dispatchServerRequest(req serverRequest) {
	switch req.Method {
	case "elicitation/create":
		c.handleElicitationCreate(req)
	default:
		c.respondServerError(req.ID, rpcMethodNotFound, "Method not found")
	}
}

// handleElicitationCreate decodes the request params, invokes the installed
// ElicitationHandler (or auto-declines if none is set), and sends the
// result back to the server with the matching id. Malformed params (fail
// to decode) get a JSON-RPC -32602 "Invalid params" error reply, mirroring
// the -32601 "Method not found" path for unknown methods. A handler error
// (the installed ElicitationHandler itself returning err) is reported to
// the server as a JSON-RPC -32603 "Internal error" reply, not a synthesized
// Action "cancel" result: "cancel"/"decline" are reserved for a genuine
// ElicitationResult the handler returned, so the server (and anything it
// reports to a human) can tell a handler bug apart from a deliberate user
// cancellation.
func (c *Client) handleElicitationCreate(req serverRequest) {
	c.mu.Lock()
	h := c.elicitationHandler
	c.mu.Unlock()

	if h == nil {
		c.respondServerResult(req.ID, elicitationCreateResultWire{Action: "decline"})
		return
	}

	var params elicitationCreateParamsWire
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.respondServerError(req.ID, rpcInvalidParams, "Invalid params")
		return
	}

	result, err := h(c.ctx, ElicitationRequest{
		Message:         params.Message,
		RequestedSchema: params.RequestedSchema,
	})
	if err != nil {
		c.respondServerError(req.ID, rpcInternalError, "Internal error")
		return
	}
	c.respondServerResult(req.ID, elicitationCreateResultWire{
		Action:  result.Action,
		Content: result.Content,
	})
}
