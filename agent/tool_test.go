package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestAsToolSchema verifies AsTool's hand-written schema matches the spec
// exactly, with the sub-agent's name interpolated into the description.
func TestAsToolSchema(t *testing.T) {
	sub := &Agent{Model: &aitest.MockModel{}}
	tool := AsTool(sub, "researcher", "delegates research tasks")

	if tool.Name() != "researcher" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() != "delegates research tasks" {
		t.Fatalf("Description = %q", tool.Description())
	}

	want := `{"type":"object","properties":{"task":{"type":"string","description":"The task for the researcher agent."}},"required":["task"],"additionalProperties":false}`
	var gotJSON, wantJSON any
	if err := json.Unmarshal(tool.Schema(), &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatal(err)
	}
	gotBytes, _ := json.Marshal(gotJSON)
	wantBytes, _ := json.Marshal(wantJSON)
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("Schema = %s, want %s", tool.Schema(), want)
	}
}

// TestAsToolExecutesSubAgentAndReturnsText verifies AsTool runs a scripted
// sub-agent loop (including its own tool call) and returns the sub-agent's
// final text.
func TestAsToolExecutesSubAgentAndReturnsText(t *testing.T) {
	sub := &Agent{
		Model: &aitest.MockModel{Responses: []*provider.Response{
			toolCallResponse("t", "1", `{}`),
			textResponse("sub-agent final answer"),
		}},
		Tools: []ai.Tool{echoTool("t")},
	}
	tool := AsTool(sub, "helper", "a helper agent")

	res, err := tool.Execute(context.Background(), []byte(`{"task":"do the thing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res != "sub-agent final answer" {
		t.Fatalf("res = %v", res)
	}
}

type outAnswer struct {
	Answer string `json:"answer"`
}

// TestAsToolWithOutputReturnsDecodedValue verifies that when the sub-agent
// has an Output set, AsTool returns the decoded Output rather than Text.
func TestAsToolWithOutputReturnsDecodedValue(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{
			{
				Content:      []provider.ContentPart{provider.TextPart{Text: `{"answer":"42"}`}},
				FinishReason: provider.FinishStop,
			},
		},
	}
	sub := &Agent{
		Model:  m,
		Output: ai.OutputObject[outAnswer](),
	}
	tool := AsTool(sub, "helper", "a helper agent")

	res, err := tool.Execute(context.Background(), []byte(`{"task":"answer"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := res.(outAnswer)
	if !ok {
		t.Fatalf("res = %#v (%T), want outAnswer", res, res)
	}
	if got.Answer != "42" {
		t.Fatalf("Answer = %q", got.Answer)
	}
}

// TestAsToolSubAgentErrorPropagates verifies a sub-agent failure surfaces as
// the tool's execution error.
func TestAsToolSubAgentErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	sub := &Agent{Model: &aitest.MockModel{Err: wantErr}}
	tool := AsTool(sub, "helper", "a helper agent")

	_, err := tool.Execute(context.Background(), []byte(`{"task":"x"}`))
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

// TestAsToolBadArgsIsError verifies malformed args to the generated tool
// return an error rather than panicking.
func TestAsToolBadArgsIsError(t *testing.T) {
	sub := &Agent{Model: &aitest.MockModel{}}
	tool := AsTool(sub, "helper", "a helper agent")

	if _, err := tool.Execute(context.Background(), []byte(`not json`)); err == nil {
		t.Fatal("want error for malformed args")
	}
}
