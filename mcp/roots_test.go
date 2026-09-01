package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRootsListReturnsConfiguredRoots(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	c.SetRoots([]Root{{URI: "file:///tmp/p", Name: "p"}})

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 31, "roots/list", map[string]any{})

	resp := recvServerResponse(t, server)
	if resp.ID != 31 {
		t.Fatalf("ID = %d, want 31", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("Error = %+v, want nil", resp.Error)
	}
	var result struct {
		Roots []Root `json:"roots"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Roots) != 1 {
		t.Fatalf("Roots = %+v, want 1 root", result.Roots)
	}
	if result.Roots[0].URI != "file:///tmp/p" || result.Roots[0].Name != "p" {
		t.Fatalf("Roots[0] = %+v, want {file:///tmp/p p}", result.Roots[0])
	}
}

func TestRootsListWithoutSetRootsRespondsMethodNotFound(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	initializeWithCaps(t, client, server, c, map[string]any{})

	sendServerRequest(t, server, 32, "roots/list", map[string]any{})

	resp := recvServerResponse(t, server)
	if resp.ID != 32 {
		t.Fatalf("ID = %d, want 32", resp.ID)
	}
	if resp.Error == nil {
		t.Fatalf("Error = nil, want -32601")
	}
	if resp.Error.Code != rpcMethodNotFound {
		t.Fatalf("Error.Code = %d, want %d", resp.Error.Code, rpcMethodNotFound)
	}
}

func TestInitializeDeclaresRootsWithListChangedFalseWhenSet(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer func() { _ = c.Close() }()

	c.SetRoots([]Root{{URI: "file:///tmp/p", Name: "p"}})

	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	var params struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	raw, ok := params.Capabilities["roots"]
	if !ok {
		t.Fatalf("capabilities did not declare roots with roots set: %+v", params.Capabilities)
	}
	var rootsCap struct {
		ListChanged bool `json:"listChanged"`
	}
	if err := json.Unmarshal(raw, &rootsCap); err != nil {
		t.Fatalf("decode roots capability: %v", err)
	}
	if rootsCap.ListChanged != false {
		t.Fatalf("listChanged = %v, want false", rootsCap.ListChanged)
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

func TestInitializeDoesNotDeclareRootsWithoutSetRoots(t *testing.T) {
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
	if _, ok := params.Capabilities["roots"]; ok {
		t.Fatalf("capabilities declared roots without SetRoots: %+v", params.Capabilities)
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
