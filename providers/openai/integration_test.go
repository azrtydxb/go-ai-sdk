package openai_test

// Integration test: drives ai.GenerateText and ai.StreamText through a full
// multi-step tool-calling loop against an httptest fixture that speaks the
// real OpenAI chat-completions wire format (both the plain JSON response and
// the SSE streaming response). This exercises the complete
// ai -> provider -> providers/openai round trip, including the part that
// unit tests (which drive ai.GenerateText/StreamText against
// ai/aitest.MockModel) never touch: that the SDK actually serializes the
// assistant's tool_calls and the tool results back onto the wire in the
// shape the OpenAI API expects for the next turn.
//
// providers/openai is used (rather than a package under ai/) specifically
// because it can import ai without an import cycle: ai does not import any
// providers/* package.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

// weatherArgs is the tool argument type shared by both subtests below.
type weatherArgs struct {
	City string `json:"city"`
}

// newToolLoopFixture starts an httptest server that plays the OpenAI
// chat-completions wire format for a single-tool-call loop: the first
// request (no "tool" role message yet) gets back a tool_calls response/
// stream requesting get_weather; the second request (once a "tool" role
// message is present) gets back the final text response/stream. Every
// request body is captured in order so the test can assert on the wire
// shape of the follow-up request.
func newToolLoopFixture(t *testing.T) (*httptest.Server, *[][]byte) {
	t.Helper()
	var bodies [][]byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("fixture: read request body: %v", err)
		}
		bodies = append(bodies, body)

		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("fixture: decode request body: %v", err)
		}

		hasToolMsg := false
		msgs, _ := req["messages"].([]any)
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if ok && mm["role"] == "tool" {
				hasToolMsg = true
			}
		}

		streaming, _ := req["stream"].(bool)
		if streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			if !hasToolMsg {
				io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_s1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]},"finish_reason":null}]}`+"\n\n")
				io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Ghent\"}"}}]},"finish_reason":null}]}`+"\n\n")
				io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`+"\n\n")
			} else {
				io.WriteString(w, `data: {"choices":[{"delta":{"content":"It is "},"finish_reason":null}]}`+"\n\n")
				io.WriteString(w, `data: {"choices":[{"delta":{"content":"sunny"},"finish_reason":null}]}`+"\n\n")
				io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`+"\n\n")
			}
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if !hasToolMsg {
			io.WriteString(w, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Ghent\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"content":"It is sunny","tool_calls":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &bodies
}

// requireAssistantToolCallAndToolResult asserts that body (a captured
// chat-completions request) contains an assistant message with a
// "tool_calls" array and a "tool" role message whose tool_call_id matches
// wantToolCallID — i.e. that the SDK round-tripped the tool call and its
// result back onto the wire for the follow-up turn, in the shape the OpenAI
// API requires.
func requireAssistantToolCallAndToolResult(t *testing.T, body []byte, wantToolCallID string) {
	t.Helper()

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode captured request: %v", err)
	}
	msgs, _ := req["messages"].([]any)

	foundAssistantToolCalls := false
	foundToolResult := false
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if mm["role"] == "assistant" && mm["tool_calls"] != nil {
			foundAssistantToolCalls = true
		}
		if mm["role"] == "tool" && mm["tool_call_id"] == wantToolCallID {
			foundToolResult = true
		}
	}

	if !foundAssistantToolCalls || !foundToolResult {
		t.Fatalf("second request missing assistant tool_calls (found=%v) or tool result (found=%v); body: %s",
			foundAssistantToolCalls, foundToolResult, body)
	}
}

// TestIntegrationGenerateTextToolLoop drives ai.GenerateText through a full
// multi-step tool-calling loop against the fixture: the model returns
// tool_calls, the SDK executes the tool and sends the assistant's
// tool_calls plus a role:"tool" result message back on the wire, and the
// fixture then returns the final text.
func TestIntegrationGenerateTextToolLoop(t *testing.T) {
	srv, bodies := newToolLoopFixture(t)

	model := openai.New(openai.WithAPIKey("test"), openai.WithBaseURL(srv.URL)).Model("gpt-test")

	toolCalls := 0
	weather := ai.NewTool("get_weather", "get the weather for a city", func(_ context.Context, a weatherArgs) (any, error) {
		toolCalls++
		if a.City != "Ghent" {
			return nil, fmt.Errorf("wrong city %q", a.City)
		}
		return map[string]any{"temp": 21, "sky": "sunny"}, nil
	})

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "weather in Ghent?",
		Tools:    []ai.Tool{weather},
		MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("GenerateText: %v", err)
	}

	if res.Text != "It is sunny" {
		t.Errorf("Text = %q, want %q", res.Text, "It is sunny")
	}
	if len(res.Steps) != 2 {
		t.Errorf("len(Steps) = %d, want 2", len(res.Steps))
	}
	if toolCalls != 1 {
		t.Errorf("tool executed %d times, want 1", toolCalls)
	}
	if len(res.Steps) > 0 {
		if len(res.Steps[0].ToolResults) != 1 {
			t.Fatalf("Steps[0].ToolResults = %+v, want 1 result", res.Steps[0].ToolResults)
		}
		if res.Steps[0].ToolResults[0].Err != nil {
			t.Errorf("Steps[0].ToolResults[0].Err = %v, want nil", res.Steps[0].ToolResults[0].Err)
		}
	}
	if res.Usage.TotalTokens != 39 { // 15 (step 1) + 24 (step 2)
		t.Errorf("Usage.TotalTokens = %d, want 39", res.Usage.TotalTokens)
	}

	if len(*bodies) < 2 {
		t.Fatalf("captured %d request bodies, want >= 2", len(*bodies))
	}
	requireAssistantToolCallAndToolResult(t, (*bodies)[1], "call_1")
}

// TestIntegrationStreamTextToolLoop drives ai.StreamText through the same
// multi-step tool-calling loop, but over the fixture's SSE responses.
func TestIntegrationStreamTextToolLoop(t *testing.T) {
	srv, bodies := newToolLoopFixture(t)

	model := openai.New(openai.WithAPIKey("test"), openai.WithBaseURL(srv.URL)).Model("gpt-test")

	toolCalls := 0
	weather := ai.NewTool("get_weather", "get the weather for a city", func(_ context.Context, a weatherArgs) (any, error) {
		toolCalls++
		if a.City != "Ghent" {
			return nil, fmt.Errorf("wrong city %q", a.City)
		}
		return map[string]any{"temp": 21, "sky": "sunny"}, nil
	})

	stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "weather in Ghent?",
		Tools:    []ai.Tool{weather},
		MaxSteps: 3,
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	defer stream.Close()

	var text string
	var finishes int
	for p := range stream.Parts() {
		switch pt := p.(type) {
		case provider.TextDelta:
			text += pt.Text
		case provider.FinishPart:
			finishes++
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream.Err() = %v", err)
	}

	if text != "It is sunny" {
		t.Errorf("accumulated text = %q, want %q", text, "It is sunny")
	}
	if finishes != 2 {
		t.Errorf("FinishPart count = %d, want 2 (one per step)", finishes)
	}
	if len(stream.Steps()) != 2 {
		t.Errorf("len(Steps()) = %d, want 2", len(stream.Steps()))
	}
	if toolCalls != 1 {
		t.Errorf("tool executed %d times, want 1", toolCalls)
	}
	if steps := stream.Steps(); len(steps) > 0 {
		if len(steps[0].ToolResults) != 1 {
			t.Fatalf("Steps()[0].ToolResults = %+v, want 1 result", steps[0].ToolResults)
		}
		if steps[0].ToolResults[0].Err != nil {
			t.Errorf("Steps()[0].ToolResults[0].Err = %v, want nil", steps[0].ToolResults[0].Err)
		}
	}
	if stream.Usage().TotalTokens != 24 { // 12 (step 1) + 12 (step 2), summed across steps
		t.Errorf("Usage().TotalTokens = %d, want 24", stream.Usage().TotalTokens)
	}

	if len(*bodies) < 2 {
		t.Fatalf("captured %d request bodies, want >= 2", len(*bodies))
	}
	requireAssistantToolCallAndToolResult(t, (*bodies)[1], "call_s1")
}
