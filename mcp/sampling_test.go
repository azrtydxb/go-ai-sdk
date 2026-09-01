package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSamplingHandlerCreatesMessage(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	var gotReq CreateMessageRequest
	c.SetSamplingHandler(func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
		gotReq = req
		return CreateMessageResult{
			Role:       "assistant",
			Content:    json.RawMessage(`{"type":"text","text":"hi there"}`),
			Model:      "test-model",
			StopReason: "endTurn",
		}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 21, "sampling/createMessage", map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": map[string]any{"type": "text", "text": "hello"}},
		},
		"systemPrompt":     "be nice",
		"maxTokens":        100,
		"modelPreferences": map[string]any{"hints": []map[string]any{{"name": "claude"}}},
	})

	resp := recvServerResponse(t, server)
	if resp.ID != 21 {
		t.Fatalf("ID = %d, want 21", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("Error = %+v, want nil", resp.Error)
	}
	var result struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		Model      string          `json:"model"`
		StopReason string          `json:"stopReason"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Role != "assistant" {
		t.Fatalf("Role = %q, want assistant", result.Role)
	}
	if string(result.Content) != `{"type":"text","text":"hi there"}` {
		t.Fatalf("Content = %s", result.Content)
	}
	if result.Model != "test-model" {
		t.Fatalf("Model = %q, want test-model", result.Model)
	}
	if result.StopReason != "endTurn" {
		t.Fatalf("StopReason = %q, want endTurn", result.StopReason)
	}

	if len(gotReq.Messages) != 1 {
		t.Fatalf("Messages = %+v, want 1 message", gotReq.Messages)
	}
	if gotReq.Messages[0].Role != "user" {
		t.Fatalf("Messages[0].Role = %q, want user", gotReq.Messages[0].Role)
	}
	if gotReq.SystemPrompt != "be nice" {
		t.Fatalf("SystemPrompt = %q, want %q", gotReq.SystemPrompt, "be nice")
	}
	if gotReq.MaxTokens != 100 {
		t.Fatalf("MaxTokens = %d, want 100", gotReq.MaxTokens)
	}
	if gotReq.ModelPreferences == nil {
		t.Fatalf("ModelPreferences = nil, want non-nil")
	}
}

func TestSamplingNilHandlerRespondsMethodNotFound(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 22, "sampling/createMessage", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": "hi"}}},
	})

	resp := recvServerResponse(t, server)
	if resp.ID != 22 {
		t.Fatalf("ID = %d, want 22", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32601")
	}
	if resp.Error.Code != rpcMethodNotFound {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, rpcMethodNotFound)
	}
}

func TestSamplingCreateMessageMalformedParamsRespondsInvalidParams(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	c.SetSamplingHandler(func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
		t.Fatal("handler should not be invoked for malformed params")
		return CreateMessageResult{}, nil
	})

	initializeWithCaps(t, client, server, c, map[string]any{"sampling": map[string]any{}})

	sendServerRequest(t, server, 23, "sampling/createMessage", []int{1, 2, 3})

	resp := recvServerResponse(t, server)
	if resp.ID != 23 {
		t.Fatalf("ID = %d, want 23", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32602")
	}
	if resp.Error.Code != rpcInvalidParams {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, rpcInvalidParams)
	}
	if resp.Result != nil {
		t.Fatalf("Result = %s, want nil (error reply must not also carry a result)", resp.Result)
	}
}

func TestSamplingHandlerErrorRespondsInternalError(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	c.SetSamplingHandler(func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
		return CreateMessageResult{}, errors.New("boom")
	})

	initializeWithCaps(t, client, server, c, map[string]any{"sampling": map[string]any{}})

	sendServerRequest(t, server, 24, "sampling/createMessage", map[string]any{
		"messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": "hi"}}},
	})

	resp := recvServerResponse(t, server)
	if resp.ID != 24 {
		t.Fatalf("ID = %d, want 24", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32603")
	}
	if resp.Error.Code != rpcInternalError {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, rpcInternalError)
	}
	if resp.Result != nil {
		t.Fatalf("Result = %s, want nil (error reply must not also carry a result)", resp.Result)
	}
}

func TestInitializeDeclaresSamplingWithHandler(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	c.SetSamplingHandler(func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error) {
		return CreateMessageResult{}, nil
	})

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if _, ok := params.Capabilities["sampling"]; !ok {
		t.Fatalf("capabilities did not declare sampling with a handler set: %+v", params.Capabilities)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server)
	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestInitializeDoesNotDeclareSamplingWithoutHandler(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if _, ok := params.Capabilities["sampling"]; ok {
		t.Fatalf("capabilities declared sampling without a handler: %+v", params.Capabilities)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
	})
	recvRequest(t, server)
	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}
