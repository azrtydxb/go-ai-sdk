package ai

import (
	"context"
	"reflect"
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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

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

// TestExtractReasoningMiddleware_Generate_OrphanCloseTagIsInert verifies
// that with the default StartWithReasoning=false, a closing tag with no
// matching opener is inert: it passes through as ordinary text rather
// than being reclassified as reasoning.
func TestExtractReasoningMiddleware_Generate_OrphanCloseTagIsInert(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{
				provider.TextPart{Text: "no opener here</think> the answer"},
			}},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content = %#v", resp.Content)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok || tp.Text != "no opener here</think> the answer" {
		t.Errorf("Content[0] = %#v, want unmodified TextPart", resp.Content[0])
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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think", StartWithReasoning: true})

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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

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
		if !reflect.DeepEqual(parts[i], want[i]) {
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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

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
		if !reflect.DeepEqual(parts[i], want[i]) {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

// TestExtractReasoningMiddleware_Stream_OrphanCloseTagIsInert verifies
// that, streaming, a closing tag with no matching opener passes through
// as ordinary TextDelta content (default StartWithReasoning=false), and
// that non-tag content around it is still delivered incrementally rather
// than buffered up.
func TestExtractReasoningMiddleware_Stream_OrphanCloseTagIsInert(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "no opener "},
				provider.TextDelta{Text: "here</th"},
				provider.TextDelta{Text: "ink> answer"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	// Outside (not-yet-reasoning) state only ever watches for the OPENING
	// tag, so "</th"/"ink>" never look like a candidate prefix of it —
	// each delta is therefore flushed as its own TextDelta immediately,
	// verbatim, with no buffering at all, proving the scanner isn't
	// holding anything back waiting to see whether a stray close tag
	// shows up.
	want := []provider.StreamPart{
		provider.TextDelta{Text: "no opener "},
		provider.TextDelta{Text: "here</th"},
		provider.TextDelta{Text: "ink> answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if !reflect.DeepEqual(parts[i], want[i]) {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}
}

// TestExtractReasoningMiddleware_Stream_StartWithReasoningIncremental
// verifies that with StartWithReasoning=true, reasoning text is streamed
// incrementally as ReasoningDeltas (multiple deltas for multiple input
// TextDeltas, not buffered into one), up until the closing tag, after
// which normal text resumes.
func TestExtractReasoningMiddleware_Stream_StartWithReasoningIncremental(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "Thinking "},
				provider.TextDelta{Text: "without tag"},
				provider.TextDelta{Text: "</think> Answer"},
				provider.FinishPart{Reason: provider.FinishStop},
			},
		},
	}
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think", StartWithReasoning: true})

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	want := []provider.StreamPart{
		provider.ReasoningDelta{Text: "Thinking "},
		provider.ReasoningDelta{Text: "without tag"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "Thinking without tag"}},
		provider.TextDelta{Text: " Answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if !reflect.DeepEqual(parts[i], want[i]) {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}

	var reasoningDeltaCount int
	for _, p := range parts {
		if _, ok := p.(provider.ReasoningDelta); ok {
			reasoningDeltaCount++
		}
	}
	if reasoningDeltaCount < 2 {
		t.Errorf("got %d ReasoningDelta parts, want at least 2 (incremental streaming)", reasoningDeltaCount)
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
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	// Neither delta contains a "<" so both flow through immediately and
	// incrementally as separate TextDeltas — no buffering pending
	// resolution of whether a tag might show up later.
	want := []provider.StreamPart{
		provider.TextDelta{Text: "hello "},
		provider.TextDelta{Text: "world"},
		provider.FinishPart{Reason: provider.FinishStop},
	}
	if len(parts) != len(want) {
		t.Fatalf("parts = %#v, want %#v", parts, want)
	}
	for i := range want {
		if !reflect.DeepEqual(parts[i], want[i]) {
			t.Errorf("parts[%d] = %#v, want %#v", i, parts[i], want[i])
		}
	}

	var textDeltaCount int
	for _, p := range parts {
		if _, ok := p.(provider.TextDelta); ok {
			textDeltaCount++
		}
	}
	if textDeltaCount < 2 {
		t.Errorf("got %d TextDelta parts, want at least 2 (incremental streaming)", textDeltaCount)
	}
}

func TestExtractReasoningMiddleware_PassthroughFields(t *testing.T) {
	mock := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true}}
	wrapped := ExtractReasoningMiddleware(mock, ExtractReasoningOpts{TagName: "think"})
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
		if !reflect.DeepEqual(parts[i], want[i]) {
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

// TestDefaultSettingsMiddleware_FillsNewScalarFields covers the
// TopK/PresencePenalty/FrequencyPenalty/Seed nil-pointer fills added
// alongside Temperature/TopP/MaxTokens.
func TestDefaultSettingsMiddleware_FillsNewScalarFields(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defTopK := 40
	defPresence := 0.3
	defFrequency := 0.4
	defSeed := int64(123)
	defaults := provider.Call{
		TopK:             &defTopK,
		PresencePenalty:  &defPresence,
		FrequencyPenalty: &defFrequency,
		Seed:             &defSeed,
	}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	_, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mock.Calls[0]
	if got.TopK == nil || *got.TopK != defTopK {
		t.Errorf("TopK = %v, want %v", got.TopK, defTopK)
	}
	if got.PresencePenalty == nil || *got.PresencePenalty != defPresence {
		t.Errorf("PresencePenalty = %v, want %v", got.PresencePenalty, defPresence)
	}
	if got.FrequencyPenalty == nil || *got.FrequencyPenalty != defFrequency {
		t.Errorf("FrequencyPenalty = %v, want %v", got.FrequencyPenalty, defFrequency)
	}
	if got.Seed == nil || *got.Seed != defSeed {
		t.Errorf("Seed = %v, want %v", got.Seed, defSeed)
	}
}

// TestDefaultSettingsMiddleware_PerCallScalarFieldsWin covers that a
// per-call TopK/PresencePenalty/FrequencyPenalty/Seed (already set, non-nil)
// is preserved rather than overwritten by the matching default.
func TestDefaultSettingsMiddleware_PerCallScalarFieldsWin(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defTopK := 40
	defPresence := 0.3
	defFrequency := 0.4
	defSeed := int64(123)
	defaults := provider.Call{
		TopK:             &defTopK,
		PresencePenalty:  &defPresence,
		FrequencyPenalty: &defFrequency,
		Seed:             &defSeed,
	}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	callTopK := 5
	callPresence := 0.9
	callFrequency := 0.8
	callSeed := int64(999)
	call := provider.Call{
		TopK:             &callTopK,
		PresencePenalty:  &callPresence,
		FrequencyPenalty: &callFrequency,
		Seed:             &callSeed,
	}
	_, err := wrapped.Generate(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mock.Calls[0]
	if got.TopK == nil || *got.TopK != callTopK {
		t.Errorf("TopK = %v, want %v (per-call should win)", got.TopK, callTopK)
	}
	if got.PresencePenalty == nil || *got.PresencePenalty != callPresence {
		t.Errorf("PresencePenalty = %v, want %v (per-call should win)", got.PresencePenalty, callPresence)
	}
	if got.FrequencyPenalty == nil || *got.FrequencyPenalty != callFrequency {
		t.Errorf("FrequencyPenalty = %v, want %v (per-call should win)", got.FrequencyPenalty, callFrequency)
	}
	if got.Seed == nil || *got.Seed != callSeed {
		t.Errorf("Seed = %v, want %v (per-call should win)", got.Seed, callSeed)
	}
}

// TestDefaultSettingsMiddleware_HeadersMerge covers Headers' per-key merge
// semantics: defaults provide missing keys, per-call keys win on conflicts.
func TestDefaultSettingsMiddleware_HeadersMerge(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defaults := provider.Call{
		Headers: map[string]string{
			"x-default-only": "def",
			"x-conflict":     "from-default",
		},
	}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	call := provider.Call{
		Headers: map[string]string{
			"x-call-only": "call",
			"x-conflict":  "from-call",
		},
	}
	_, err := wrapped.Generate(context.Background(), call)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mock.Calls[0].Headers
	want := map[string]string{
		"x-default-only": "def",
		"x-call-only":    "call",
		"x-conflict":     "from-call",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Headers = %#v, want %#v", got, want)
	}

	// The caller's input maps must not have been mutated or aliased.
	if defaults.Headers["x-conflict"] != "from-default" {
		t.Error("defaults.Headers mutated by applyDefaults")
	}
	if call.Headers["x-conflict"] != "from-call" {
		t.Error("call.Headers mutated by applyDefaults")
	}
}

// TestDefaultSettingsMiddleware_HeadersDefaultsOnly covers the case where
// only defaults carry Headers: the per-call Headers map (nil) is filled in
// wholesale from defaults.
func TestDefaultSettingsMiddleware_HeadersDefaultsOnly(t *testing.T) {
	mock := &aitest.MockModel{Responses: []*provider.Response{{}}}
	defaults := provider.Call{Headers: map[string]string{"x-default-only": "def"}}
	wrapped := DefaultSettingsMiddleware(mock, defaults)

	_, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := mock.Calls[0].Headers
	want := map[string]string{"x-default-only": "def"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Headers = %#v, want %#v", got, want)
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

// TestMergeProviderOptions_DefensiveCopy covers that mergeProviderOptions
// never hands back a map aliasing the caller's defaults or override maps:
// mutating the merged result (both the top-level map and a namespace map
// within it) must not be observable through either input afterward.
func TestMergeProviderOptions_DefensiveCopy(t *testing.T) {
	defaults := map[string]any{
		"openai": map[string]any{"seed": 42},
	}
	override := map[string]any{
		"anthropic": map[string]any{"top_k": 5},
	}

	merged := mergeProviderOptions(defaults, override)

	// Mutate the merged top-level map: add a namespace and overwrite an
	// existing one.
	merged["new_provider"] = map[string]any{"x": 1}
	merged["anthropic"] = "clobbered"

	// Mutate a namespace map reached through the merged result.
	openaiOpts := merged["openai"].(map[string]any)
	openaiOpts["seed"] = 999
	openaiOpts["extra"] = "added"

	if _, ok := defaults["new_provider"]; ok {
		t.Error("mutating merged added a key visible in defaults")
	}
	if _, ok := override["new_provider"]; ok {
		t.Error("mutating merged added a key visible in override")
	}
	if _, ok := override["anthropic"].(map[string]any); !ok {
		t.Error("mutating merged[\"anthropic\"] clobbered override's anthropic map")
	}
	defOpenai := defaults["openai"].(map[string]any)
	if defOpenai["seed"] != 42 {
		t.Errorf("defaults[openai][seed] = %v, want 42 (unaffected by mutating merged result)", defOpenai["seed"])
	}
	if _, ok := defOpenai["extra"]; ok {
		t.Error("defaults[openai] gained a key added to the merged result's namespace map")
	}
}

// TestMergeProviderOptions_DefensiveCopy_DefaultsOnly covers the fast path
// where override is empty: the returned map must still be a fresh copy of
// defaults, not defaults itself.
func TestMergeProviderOptions_DefensiveCopy_DefaultsOnly(t *testing.T) {
	defaults := map[string]any{
		"openai": map[string]any{"seed": 42},
	}
	merged := mergeProviderOptions(defaults, nil)
	merged["openai"].(map[string]any)["seed"] = 999
	merged["new_provider"] = "x"

	if defaults["openai"].(map[string]any)["seed"] != 42 {
		t.Error("mutating merged (defaults-only path) affected defaults' namespace map")
	}
	if _, ok := defaults["new_provider"]; ok {
		t.Error("mutating merged (defaults-only path) added a key visible in defaults")
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
