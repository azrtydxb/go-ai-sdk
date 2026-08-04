package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestToolsSchemaPassthrough(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	wantSchema := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)

	type result struct {
		tools []ai.Tool
		err   error
	}
	results := make(chan result, 1)
	go func() {
		tools, err := Tools(context.Background(), c)
		results <- result{tools, err}
	}()

	req := recvRequest(t, server)
	if req.Method != "tools/list" {
		t.Fatalf("method = %q", req.Method)
	}
	sendResult(t, server, *req.ID, map[string]any{
		"tools": []map[string]any{
			{"name": "get_weather", "description": "gets the weather", "inputSchema": json.RawMessage(wantSchema)},
		},
	})

	r := <-results
	if r.err != nil {
		t.Fatalf("Tools: %v", r.err)
	}
	if len(r.tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(r.tools))
	}
	tool := r.tools[0]
	if tool.Name() != "get_weather" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() != "gets the weather" {
		t.Errorf("Description() = %q", tool.Description())
	}
	if string(tool.Schema()) != string(wantSchema) {
		t.Errorf("Schema() = %s, want %s (byte-identical passthrough)", tool.Schema(), wantSchema)
	}
}

func TestToolsExecuteHappyPath(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	results := make(chan struct {
		tools []ai.Tool
		err   error
	}, 1)
	go func() {
		tools, err := Tools(context.Background(), c)
		results <- struct {
			tools []ai.Tool
			err   error
		}{tools, err}
	}()
	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"tools": []map[string]any{
			{"name": "echo", "description": "", "inputSchema": json.RawMessage(`{}`)},
		},
	})
	r := <-results
	if r.err != nil {
		t.Fatalf("Tools: %v", r.err)
	}
	tool := r.tools[0]

	type execResult struct {
		val any
		err error
	}
	execResults := make(chan execResult, 1)
	go func() {
		v, err := tool.Execute(context.Background(), json.RawMessage(`{"msg":"hi"}`))
		execResults <- execResult{v, err}
	}()

	callReq := recvRequest(t, server)
	if callReq.Method != "tools/call" {
		t.Fatalf("method = %q", callReq.Method)
	}
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(callReq.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "echo" {
		t.Fatalf("name = %q", params.Name)
	}
	if string(params.Arguments) != `{"msg":"hi"}` {
		t.Fatalf("arguments = %s, want raw passthrough", params.Arguments)
	}
	sendResult(t, server, *callReq.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "echoed: hi"}},
		"isError": false,
	})

	er := <-execResults
	if er.err != nil {
		t.Fatalf("Execute: %v", er.err)
	}
	if er.val != "echoed: hi" {
		t.Fatalf("Execute result = %v, want %q", er.val, "echoed: hi")
	}
}

func TestToolsExecuteIsErrorBecomesGoError(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	results := make(chan []ai.Tool, 1)
	go func() {
		tools, err := Tools(context.Background(), c)
		if err != nil {
			t.Errorf("Tools: %v", err)
		}
		results <- tools
	}()
	req := recvRequest(t, server)
	sendResult(t, server, *req.ID, map[string]any{
		"tools": []map[string]any{{"name": "boom", "description": "", "inputSchema": json.RawMessage(`{}`)}},
	})
	tool := (<-results)[0]

	execResults := make(chan error, 1)
	go func() {
		_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
		execResults <- err
	}()
	callReq := recvRequest(t, server)
	sendResult(t, server, *callReq.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "it broke"}},
		"isError": true,
	})

	err := <-execResults
	if err == nil {
		t.Fatal("Execute: want error for IsError result, got nil")
	}
	if !strings.Contains(err.Error(), "it broke") {
		t.Fatalf("err = %v, want it to contain the tool's error text", err)
	}
	var toolErr *ai.ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("err = %v, want *ai.ToolExecutionError", err)
	}
	if toolErr.ToolName != "boom" {
		t.Fatalf("ToolName = %q, want %q", toolErr.ToolName, "boom")
	}
	if !strings.Contains(toolErr.Cause.Error(), "it broke") {
		t.Fatalf("Cause = %v, want it to contain the tool's error text", toolErr.Cause)
	}
}

// TestToolsIntegrationWithGenerateText exercises the full path: a MockModel
// requests an MCP-backed tool, ai.GenerateText's tool loop executes it via
// mcp.Tools' adapter against a scripted MCP server, and the loop continues
// with the result.
func TestToolsIntegrationWithGenerateText(t *testing.T) {
	c, server := withCap("tools")
	defer c.Close()

	// Serve tools/list once, then tools/call once, on a background
	// goroutine, mirroring a real (if trivial) MCP server.
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		req := recvRequest(t, server)
		if req.Method != "tools/list" {
			t.Errorf("method = %q, want tools/list", req.Method)
			return
		}
		sendResult(t, server, *req.ID, map[string]any{
			"tools": []map[string]any{
				{"name": "get_weather", "description": "gets the weather", "inputSchema": json.RawMessage(`{"type":"object"}`)},
			},
		})

		callReq := recvRequest(t, server)
		if callReq.Method != "tools/call" {
			t.Errorf("method = %q, want tools/call", callReq.Method)
			return
		}
		sendResult(t, server, *callReq.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "sunny"}},
			"isError": false,
		})
	}()

	tools, err := Tools(context.Background(), c)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}

	m := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`),
			}},
			FinishReason: provider.FinishToolCalls,
		},
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "It is sunny."}},
			FinishReason: provider.FinishStop,
		},
	}}

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: tools, MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("GenerateText: %v", err)
	}
	if res.Text != "It is sunny." {
		t.Fatalf("Text = %q", res.Text)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("tool result = %v, want %q", res.Steps[0].ToolResults[0].Result, "sunny")
	}

	<-serverDone
}
