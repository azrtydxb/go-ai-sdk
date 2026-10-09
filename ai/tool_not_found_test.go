package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func notFoundTool() Tool {
	return NewTool("real_tool", "", func(_ context.Context, _ weatherArgs) (any, error) { return "ok", nil })
}

// assertUnknownToolReported checks the second model call carried an error
// tool result for the bogus call that lists the available tools.
func assertUnknownToolReported(t *testing.T, calls []provider.Call) {
	t.Helper()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	msgs := calls[1].Messages
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleTool || len(last.Content) != 1 {
		t.Fatalf("last message = %+v, want one tool result", last)
	}
	tr, ok := last.Content[0].(provider.ToolResultPart)
	if !ok {
		t.Fatalf("part = %T, want ToolResultPart", last.Content[0])
	}
	if !tr.IsError || tr.ToolCallID != "c1" {
		t.Fatalf("result = %+v, want IsError for c1", tr)
	}
	s, _ := tr.Result.(string)
	if !strings.Contains(s, "no such tool: bogus") || !strings.Contains(s, "real_tool") {
		t.Fatalf("result text = %q", s)
	}
}

func TestGenerateTextUnknownToolReported(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{
		toolCallResponse("bogus", "c1", `{}`),
		{Content: []provider.ContentPart{provider.TextPart{Text: "done"}}, FinishReason: provider.FinishStop},
	}}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{notFoundTool()}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "done" || len(res.Steps) != 2 {
		t.Fatalf("text=%q steps=%d", res.Text, len(res.Steps))
	}
	var nst *NoSuchToolError
	if !errors.As(res.Steps[0].ToolResults[0].Err, &nst) {
		t.Fatalf("step result err = %v", res.Steps[0].ToolResults[0].Err)
	}
	assertUnknownToolReported(t, m.Calls)
}

func TestStreamTextUnknownToolReported(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "bogus", Args: []byte(`{}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "done"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{notFoundTool()}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatalf("Err() = %v", s.Err())
	}
	if s.Text() != "done" {
		t.Fatalf("text = %q", s.Text())
	}
	assertUnknownToolReported(t, m.Calls)
}

func TestGenerateTextUnknownToolFailPolicy(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{toolCallResponse("bogus", "c1", `{}`)}}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{notFoundTool()}, MaxSteps: 3, ToolNotFound: ToolNotFoundFail})
	var nst *NoSuchToolError
	if !errors.As(err, &nst) || nst.ToolName != "bogus" {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamTextUnknownToolFailPolicy(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "bogus", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{notFoundTool()}, MaxSteps: 3, ToolNotFound: ToolNotFoundFail})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	var nst *NoSuchToolError
	if !errors.As(s.Err(), &nst) || nst.ToolName != "bogus" {
		t.Fatalf("Err() = %v", s.Err())
	}
}
