package mcp

import (
	"context"
	"encoding/json"
)

// SamplingMessage is one message in a server-initiated
// "sampling/createMessage" request or its reply: a role and a wire content
// object (e.g. {"type":"text","text":...}), preserved verbatim.
type SamplingMessage struct {
	Role    string          // "user" | "assistant"
	Content json.RawMessage // wire content object: {"type":"text","text":...} etc.
}

// CreateMessageRequest is the payload of a server-initiated
// "sampling/createMessage" request: the server is asking the client to run
// an LLM completion on its behalf.
type CreateMessageRequest struct {
	Messages         []SamplingMessage
	SystemPrompt     string
	MaxTokens        int
	ModelPreferences json.RawMessage // passed through verbatim; nil if absent
}

// CreateMessageResult is the client's reply to a CreateMessageRequest.
type CreateMessageResult struct {
	Role       string          // typically "assistant"
	Content    json.RawMessage // wire content object
	Model      string
	StopReason string
}

// SamplingHandler is called when the server sends a
// "sampling/createMessage" request. A nil handler installed on the Client
// causes "sampling/createMessage" requests to be rejected with a JSON-RPC
// -32601 "Method not found" error and the "sampling" capability is not
// declared during Initialize.
//
// Implementations must respect ctx: it is cancelled when the Client is
// closed, and a handler that ignores cancellation and blocks indefinitely
// will not hang Client.Close forever, but it will delay it — Close waits up
// to a short grace period (see closeDrainGrace) for in-flight handlers to
// finish before giving up and returning anyway.
type SamplingHandler func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error)

// SetSamplingHandler installs h as the handler invoked for
// "sampling/createMessage" requests from the server. Call this before
// Initialize: the "sampling" capability is only declared to the server when
// a handler has been set.
func (c *Client) SetSamplingHandler(h SamplingHandler) {
	c.mu.Lock()
	c.samplingHandler = h
	c.mu.Unlock()
}

// samplingMessageWire is the wire shape of one message in
// "sampling/createMessage" params or result.
type samplingMessageWire struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// createMessageParamsWire is the wire shape of "sampling/createMessage"
// request params.
type createMessageParamsWire struct {
	Messages         []samplingMessageWire `json:"messages"`
	SystemPrompt     string                `json:"systemPrompt"`
	MaxTokens        int                   `json:"maxTokens"`
	ModelPreferences json.RawMessage       `json:"modelPreferences"`
}

// createMessageResultWire is the wire shape of the client's reply to
// "sampling/createMessage".
type createMessageResultWire struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stopReason"`
}

// handleSamplingCreateMessage decodes the request params, invokes the
// installed SamplingHandler, and sends the result back to the server with
// the matching id. A nil handler is unreachable in practice — the
// "sampling" capability is only declared when a handler is installed — but
// is answered with -32601 "Method not found" for defense in depth,
// mirroring the unknown-method path in dispatchServerRequest. Malformed
// params (fail to decode) get a JSON-RPC -32602 "Invalid params" error
// reply. A handler error (the installed SamplingHandler itself returning
// err) is reported to the server as a JSON-RPC -32603 "Internal error"
// reply.
func (c *Client) handleSamplingCreateMessage(req serverRequest) {
	c.mu.Lock()
	h := c.samplingHandler
	c.mu.Unlock()

	if h == nil {
		c.respondServerError(req.ID, rpcMethodNotFound, "Method not found")
		return
	}

	var params createMessageParamsWire
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.respondServerError(req.ID, rpcInvalidParams, "Invalid params")
		return
	}

	messages := make([]SamplingMessage, len(params.Messages))
	for i, m := range params.Messages {
		messages[i] = SamplingMessage{Role: m.Role, Content: m.Content}
	}
	result, err := h(c.ctx, CreateMessageRequest{
		Messages:         messages,
		SystemPrompt:     params.SystemPrompt,
		MaxTokens:        params.MaxTokens,
		ModelPreferences: params.ModelPreferences,
	})
	if err != nil {
		c.respondServerError(req.ID, rpcInternalError, "Internal error")
		return
	}
	c.respondServerResult(req.ID, createMessageResultWire{
		Role:       result.Role,
		Content:    result.Content,
		Model:      result.Model,
		StopReason: result.StopReason,
	})
}
