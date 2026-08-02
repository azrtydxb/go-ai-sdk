package providertest

// Selftest for the providertest harness itself: wire a tiny in-memory
// scenario model through Run and confirm the whole matrix passes. This is
// the harness's own test evidence — Tasks 13-15 will call Run against real
// fixture-server-backed models and trust that Run's assertions are correct
// because this selftest exercises every one of them.

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// scenarioModel is a minimal provider.LanguageModel that dispatches on the
// text of the last user message in the call, mirroring how a real fixture
// HTTP server is expected to behave.
type scenarioModel struct {
	name string
}

func (m *scenarioModel) ModelID() string                     { return "scenario-model" }
func (m *scenarioModel) ProviderName() string                { return m.name }
func (m *scenarioModel) Capabilities() provider.Capabilities { return provider.Capabilities{} }

func lastUserText(call provider.Call) string {
	for i := len(call.Messages) - 1; i >= 0; i-- {
		msg := call.Messages[i]
		if msg.Role != provider.RoleUser {
			continue
		}
		for _, part := range msg.Content {
			if tp, ok := part.(provider.TextPart); ok {
				return tp.Text
			}
		}
	}
	return ""
}

func (m *scenarioModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch lastUserText(call) {
	case "simple":
		return &provider.Response{
			Content:      []provider.ContentPart{provider.TextPart{Text: fmt.Sprintf("Hello from %s!", m.name)}},
			FinishReason: provider.FinishStop,
			Usage:        provider.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
			Raw:          json.RawMessage(`{"scenario":"simple"}`),
		}, nil
	case "tool":
		return &provider.Response{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID:   "call_1",
				Name: "get_weather",
				Args: json.RawMessage(`{"city":"Ghent"}`),
			}},
			FinishReason: provider.FinishToolCalls,
			Usage:        provider.Usage{InputTokens: 5, OutputTokens: 6, TotalTokens: 11},
			Raw:          json.RawMessage(`{"scenario":"tool"}`),
		}, nil
	case "fail 429":
		return nil, &ai.APICallError{StatusCode: 429, Retryable: true, Message: "rate limited"}
	case "fail 400":
		return nil, &ai.APICallError{StatusCode: 400, Retryable: false, Message: "bad request"}
	default:
		return nil, fmt.Errorf("scenarioModel.Generate: unknown scenario %q", lastUserText(call))
	}
}

func (m *scenarioModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch lastUserText(call) {
	case "stream simple":
		return &scenarioStream{parts: []provider.StreamPart{
			provider.TextDelta{Text: "Hel"},
			provider.TextDelta{Text: "lo!"},
			provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 5}},
		}}, nil
	case "stream tool":
		return &scenarioStream{parts: []provider.StreamPart{
			provider.ToolCallDelta{ID: "call_1", Name: "get_weather", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "call_1", ArgsDelta: `"Ghent"}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{
				ID:   "call_1",
				Name: "get_weather",
				Args: json.RawMessage(`{"city":"Ghent"}`),
			}},
			provider.FinishPart{Reason: provider.FinishToolCalls, Usage: provider.Usage{TotalTokens: 5}},
		}}, nil
	default:
		return nil, fmt.Errorf("scenarioModel.Stream: unknown scenario %q", lastUserText(call))
	}
}

// scenarioStream implements provider.StreamResponse by replaying a fixed
// slice of parts.
type scenarioStream struct {
	parts []provider.StreamPart
}

func (s *scenarioStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for _, p := range s.parts {
			if !yield(p) {
				return
			}
		}
	}
}

func (s *scenarioStream) Err() error   { return nil }
func (s *scenarioStream) Close() error { return nil }

func TestProvidertestSelftest(t *testing.T) {
	Run(t, Config{
		Model:        &scenarioModel{name: "scenario"},
		ProviderName: "scenario",
	})
}
