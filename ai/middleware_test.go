package ai

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func collectStreamParts(t *testing.T, sr provider.StreamResponse) []provider.StreamPart {
	t.Helper()
	var parts []provider.StreamPart
	for p := range sr.Parts() {
		parts = append(parts, p)
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	return parts
}

// ---------------------------------------------------------------------
// ExtractReasoningMiddleware
// ---------------------------------------------------------------------

func TestExtractReasoningMiddleware_Generate(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{
				Content: []provider.ContentPart{
					provider.TextPart{Text: "before <think>pondering</think> after"},
				},
				FinishReason: provider.FinishStop,
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []provider.ContentPart{
		provider.TextPart{Text: "before "},
		provider.ReasoningPart{Text: "pondering"},
		provider.TextPart{Text: " after"},
	}
	if len(resp.Content) != len(want) {
		t.Fatalf("Content = %#v, want %#v", resp.Content, want)
	}
	for i := range want {
		if resp.Content[i] != want[i] {
			t.Errorf("Content[%d] = %#v, want %#v", i, resp.Content[i], want[i])
		}
	}
}

func TestExtractReasoningMiddleware_Generate_NoTag(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{provider.TextPart{Text: "just plain text"}}},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content = %#v", resp.Content)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok || tp.Text != "just plain text" {
		t.Errorf("Content[0] = %#v", resp.Content[0])
	}
}

func TestExtractReasoningMiddleware_Generate_StartWithReasoning(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{
				provider.TextPart{Text: "pondering without open tag</think> the answer"},
			}},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []provider.ContentPart{
		provider.ReasoningPart{Text: "pondering without open tag"},
		provider.TextPart{Text: " the answer"},
	}
	if len(resp.Content) != len(want) {
		t.Fatalf("Content = %#v, want %#v", resp.Content, want)
	}
	for i := range want {
		if resp.Content[i] != want[i] {
			t.Errorf("Content[%d] = %#v, want %#v", i, resp.Content[i], want[i])
		}
	}
}

func TestExtractReasoningMiddleware_Stream(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "before <think>pondering</think> after"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	want := []provider.StreamPart{
		provider.TextDelta{Text: "before "},
		provider.ReasoningDelta{Text: "pondering"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "pondering"}},
		provider.TextDelta{Text: " after"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

// TestExtractReasoningMiddleware_Stream_TagSplitAcrossDeltas verifies that
// a tag split across multiple TextDeltas (including split across the
// opening AND closing tag) is still recognized correctly.
func TestExtractReasoningMiddleware_Stream_TagSplitAcrossDeltas(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "<th"},
				provider.TextDelta{Text: "ink>Reasoning here</thi"},
				provider.TextDelta{Text: "nk> Final answer"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	want := []provider.StreamPart{
		provider.ReasoningDelta{Text: "Reasoning here"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "Reasoning here"}},
		provider.TextDelta{Text: " Final answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

func TestExtractReasoningMiddleware_Stream_StartWithReasoning(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "Thinking without tag</th"},
				provider.TextDelta{Text: "ink> Answer"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	want := []provider.StreamPart{
		provider.ReasoningDelta{Text: "Thinking without tag"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "Thinking without tag"}},
		provider.TextDelta{Text: " Answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

func TestExtractReasoningMiddleware_Stream_NoTag(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "hello "},
				provider.TextDelta{Text: "world"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, "think")

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	// No tag ever appears, so everything buffered flushes as one TextDelta
	// once the stream ends and the ambiguity resolves in favor of "plain
	// text", followed by the pass-through FinishPart.
	want := []provider.StreamPart{
		provider.TextDelta{Text: "hello world"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

func TestExtractReasoningMiddleware_PassthroughFields(t *testing.T) {
	mock := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true}}
	wrapped := ExtractReasoningMiddleware(mock, "think")
	if wrapped.ModelID() != mock.ModelID() {
		t.Errorf("ModelID mismatch")
	}
	if wrapped.ProviderName() != mock.ProviderName() {
		t.Errorf("ProviderName mismatch")
	}
	if wrapped.Capabilities() != mock.Capabilities() {
		t.Errorf("Capabilities mismatch")
	}
}

// ---------------------------------------------------------------------
// SimulateStreamingMiddleware
// ---------------------------------------------------------------------

func TestSimulateStreamingMiddleware_ReplaysToolCallResponse(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{
				Content: []provider.ContentPart{
					provider.ReasoningPart{Text: "let me check the weather"},
					provider.TextPart{Text: "Sure, checking now."},
					provider.ToolCallPart{ID: "call_1", Name: "get_weather", Args: []byte(`{"city":"nyc"}`)},
				},
				FinishReason: provider.FinishToolCalls,
				Usage:        provider.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			},
		},
	}
	wrapped := SimulateStreamingMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	want := []provider.StreamPart{
		provider.ReasoningDelta{Text: "let me check the weather"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "let me check the weather"}},
		provider.TextDelta{Text: "Sure, checking now."},
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "call_1", Name: "get_weather", Args: []byte(`{"city":"nyc"}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls, Usage: provider.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		td1, ok1 := parts[i].(provider.ToolCallEnd)
		td2, ok2 := want[i].(provider.ToolCallEnd)
		if ok1 && ok2 {
			if td1.Call.ID != td2.Call.ID || td1.Call.Name != td2.Call.Name || string(td1.Call.Args) != string(td2.Call.Args) {
				t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
			}
			continue
		}
		if parts[i] != want[i] {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

func TestSimulateStreamingMiddleware_GeneratePassesThrough(t *testing.T) {
	resp := &provider.Response{
		Content:      []provider.ContentPart{provider.TextPart{Text: "hi"}},
		FinishReason: provider.FinishStop,
	}
	mock := &aitest.MockModel{Responses: []*provider.Response{resp}}
	wrapped := SimulateStreamingMiddleware(mock)

	got, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != resp {
		t.Errorf("Generate() did not pass through to underlying model")
	}
}

func TestSimulateStreamingMiddleware_StreamPropagatesGenerateError(t *testing.T) {
	mock := &aitest.MockModel{Err: context.DeadlineExceeded}
	wrapped := SimulateStreamingMiddleware(mock)

	_, err := wrapped.Stream(context.Background(), provider.Call{})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------
// DefaultSettingsMiddleware
// ---------------------------------------------------------------------

func TestDefaultSettingsMiddleware_FillsZeroFields(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defTemp := 0.7
	defTopP := 0.9
	defMaxTokens := 256
	defaults := provider.Call{
		Temperature:   &defTemp,
		TopP:          &defTopP,
		MaxTokens:     &defMaxTokens,
		StopSequences: []string{"STOP"},
		ProviderOptions: map[string]any{
			"openai": map[string]any{"seed": 42, "user": "default-user"},
		},
	}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	_, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	got := mock.Calls[0]
	if got.Temperature == nil || *got.Temperature != defTemp {
		t.Errorf("Temperature = %v, want %v", got.Temperature, defTemp)
	}
	if got.TopP == nil || *got.TopP != defTopP {
		t.Errorf("TopP = %v, want %v", got.TopP, defTopP)
	}
	if got.MaxTokens == nil || *got.MaxTokens != defMaxTokens {
		t.Errorf("MaxTokens = %v, want %v", got.MaxTokens, defMaxTokens)
	}
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %v, want [STOP]", got.StopSequences)
	}
	opts, ok := got.ProviderOptions["openai"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderOptions[openai] missing or wrong type: %#v", got.ProviderOptions)
	}
	if opts["seed"] != 42 || opts["user"] != "default-user" {
		t.Errorf("ProviderOptions[openai] = %#v", opts)
	}
}

func TestDefaultSettingsMiddleware_PerCallWins(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defTemp := 0.7
	defMaxTokens := 256
	defaults := provider.Call{
		Temperature: &defTemp,
		MaxTokens:   &defMaxTokens,
		ProviderOptions: map[string]any{
			"openai": map[string]any{"seed": 42, "user": "default-user"},
		},
	}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	callTemp := 0.2
	call := provider.Call{
		Temperature: &callTemp,
		ProviderOptions: map[string]any{
			"openai": map[string]any{"user": "call-user"},
		},
	}
	_, err := wrapped.Generate(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mock.Calls[0]
	if got.Temperature == nil || *got.Temperature != callTemp {
		t.Errorf("Temperature = %v, want %v (per-call should win)", got.Temperature, callTemp)
	}
	if got.MaxTokens == nil || *got.MaxTokens != defMaxTokens {
		t.Errorf("MaxTokens = %v, want %v (default should fill zero value)", got.MaxTokens, defMaxTokens)
	}
	opts := got.ProviderOptions["openai"].(map[string]any)
	if opts["user"] != "call-user" {
		t.Errorf("ProviderOptions[openai][user] = %v, want call-user (per-call should win)", opts["user"])
	}
	if opts["seed"] != 42 {
		t.Errorf("ProviderOptions[openai][seed] = %v, want 42 (default should carry through)", opts["seed"])
	}
}

func TestDefaultSettingsMiddleware_Stream(t *testing.T) {
	mock := &aitest.MockModel{Streams: [][]provider.StreamPart{{provider.FinishPart{Reason: provider.FinishStop}}}}
	defTemp := 0.5
	wrapped := DefaultSettingsMiddleware(mock, provider.Call{Temperature: &defTemp})

	_, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	if mock.Calls[0].Temperature == nil || *mock.Calls[0].Temperature != defTemp {
		t.Errorf("Temperature = %v, want %v", mock.Calls[0].Temperature, defTemp)
	}
}
