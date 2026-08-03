package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func initializeWithCaps(t *testing.T, client, server *pipeTransport, c *Client, caps map[string]any) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- c.Initialize(context.Background()) }()

	req := recvRequest(t, server)
	if req.Method != "initialize" {
		t.Fatalf("method = %q, want initialize", req.Method)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    caps,
	})
	recvRequest(t, server) // notifications/initialized

	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestCompletePromptRef(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	initializeWithCaps(t, client, server, c, map[string]any{"completions": map[string]any{}})

	type result struct {
		comp *Completion
		err  error
	}
	results := make(chan result, 1)
	go func() {
		comp, err := c.Complete(context.Background(), CompletionRef{Type: "ref/prompt", Name: "greeting"}, "name", "al")
		results <- result{comp, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "completion/complete" {
		t.Fatalf("method = %q", req.Method)
	}
	var params struct {
		Ref struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"ref"`
		Argument struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"argument"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Ref.Type != "ref/prompt" || params.Ref.Name != "greeting" {
		t.Fatalf("ref = %+v", params.Ref)
	}
	if params.Argument.Name != "name" || params.Argument.Value != "al" {
		t.Fatalf("argument = %+v", params.Argument)
	}

	sendResult(t, server, *req.ID, map[string]any{
		"completion": map[string]any{
			"values":  []string{"alice", "albert"},
			"total":   2,
			"hasMore": false,
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("Complete: %v", r.err)
	}
	if len(r.comp.Values) != 2 || r.comp.Values[0] != "alice" || r.comp.Values[1] != "albert" {
		t.Fatalf("Values = %v", r.comp.Values)
	}
	if r.comp.Total != 2 || r.comp.HasMore {
		t.Fatalf("Total/HasMore = %d/%v", r.comp.Total, r.comp.HasMore)
	}
}

func TestCompleteResourceRef(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	initializeWithCaps(t, client, server, c, map[string]any{"completions": map[string]any{}})

	results := make(chan *Completion, 1)
	errs := make(chan error, 1)
	go func() {
		comp, err := c.Complete(context.Background(), CompletionRef{Type: "ref/resource", URI: "file:///{path}"}, "path", "doc")
		results <- comp
		errs <- err
	}()

	req := recvRequest(t, server)
	var params struct {
		Ref struct {
			Type string `json:"type"`
			URI  string `json:"uri"`
		} `json:"ref"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Ref.Type != "ref/resource" || params.Ref.URI != "file:///{path}" {
		t.Fatalf("ref = %+v", params.Ref)
	}

	sendResult(t, server, *req.ID, map[string]any{
		"completion": map[string]any{
			"values":  []string{"docs/a.txt"},
			"hasMore": true,
		},
	})

	comp := <-results
	if err := <-errs; err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(comp.Values) != 1 || comp.Values[0] != "docs/a.txt" {
		t.Fatalf("Values = %v", comp.Values)
	}
	if !comp.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
}

func TestCompleteCapabilityGated(t *testing.T) {
	client, server := newPipePair()
	c := NewClient(client)
	defer c.Close()

	initializeWithCaps(t, client, server, c, map[string]any{})

	_, err := c.Complete(context.Background(), CompletionRef{Type: "ref/prompt", Name: "greeting"}, "name", "al")
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v, want *CapabilityError", err)
	}
	if capErr.Capability != "completions" {
		t.Fatalf("Capability = %q", capErr.Capability)
	}
}
