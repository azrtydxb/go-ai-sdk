package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func toolCallResponse(name, id, args string) *provider.Response {
	return &provider.Response{
		Content: []provider.ContentPart{provider.ToolCallPart{
			ID: id, Name: name, Args: []byte(args)}},
		FinishReason: provider.FinishToolCalls,
		Usage:        provider.Usage{TotalTokens: 10},
	}
}

func TestToolLoopExecutesAndContinues(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "It is sunny."}},
			FinishReason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
	}}
	tool := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) {
		return "sunny", nil
	})
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "It is sunny." {
		t.Fatalf("Text = %q", res.Text)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	if res.Steps[0].ToolResults[0].Result != "sunny" {
		t.Fatal("tool result missing")
	}
	if res.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", res.Usage)
	}

	// second call must include assistant tool-call msg + tool result msg
	second := m.Calls[1].Messages
	if second[len(second)-1].Role != provider.RoleTool {
		t.Fatal("tool msg not appended")
	}
}

func TestToolLoopStopsAtMaxSteps(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"a"}`),
		toolCallResponse("t", "c2", `{"city":"b"}`),
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(res.Steps))
	}
	if res.FinishReason != provider.FinishToolCalls {
		t.Fatalf("finish = %v", res.FinishReason)
	}
}

func TestToolLoopUnknownTool(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("nope", "c1", `{}`)}}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x",
		Tools: []Tool{NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return nil, nil })}})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestToolLoopRecordsToolErrorAndContinues(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("t", "c1", `{"city":"x"}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "sorry"}},
			FinishReason: provider.FinishStop},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) {
		return nil, errors.New("db down")
	})
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].ToolResults[0].Err == nil {
		t.Fatal("tool error not recorded")
	}
	// and the tool message sent to the model marks IsError
	msgs := m.Calls[1].Messages
	tr := msgs[len(msgs)-1].Content[0].(provider.ToolResultPart)
	if !tr.IsError {
		t.Fatal("IsError not set on tool result part")
	}
}
