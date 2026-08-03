package ai

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestStreamTextYieldsDeltasAndAccumulates(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hel"},
		provider.TextDelta{Text: "lo"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for p := range s.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if got != "hello" || s.Text() != "hello" {
		t.Fatalf("got %q text %q", got, s.Text())
	}
	if s.FinishReason() != provider.FinishStop {
		t.Fatalf("finish = %v", s.FinishReason())
	}
	if s.Usage().TotalTokens != 4 {
		t.Fatalf("usage = %+v", s.Usage())
	}
}

// TestStreamTextReasoningDeltaOnly covers a provider that only ever emits
// ReasoningDelta (no ReasoningEnd) — e.g. openaicompat reasoning_content:
// the accumulated text becomes both TextStream.ReasoningText() and a single
// synthesized ReasoningPart in the step's Response, and must not leak into
// Text().
func TestStreamTextReasoningDeltaOnly(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ReasoningDelta{Text: "let me "},
		provider.ReasoningDelta{Text: "think..."},
		provider.TextDelta{Text: "42"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	var reasoningDeltas string
	for p := range s.Parts() {
		if d, ok := p.(provider.ReasoningDelta); ok {
			reasoningDeltas += d.Text
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if reasoningDeltas != "let me think..." {
		t.Fatalf("streamed ReasoningDelta text = %q", reasoningDeltas)
	}
	if s.ReasoningText() != "let me think..." {
		t.Fatalf("ReasoningText() = %q, want %q", s.ReasoningText(), "let me think...")
	}
	if s.Text() != "42" {
		t.Fatalf("Text() = %q, want %q (reasoning must not leak)", s.Text(), "42")
	}

	steps := s.Steps()
	if len(steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(steps))
	}
	if steps[0].ReasoningText != "let me think..." {
		t.Fatalf("Steps[0].ReasoningText = %q", steps[0].ReasoningText)
	}
	if steps[0].Response == nil {
		t.Fatal("Steps[0].Response = nil, want assembled Response")
	}
	if got := steps[0].Response.ReasoningText(); got != "let me think..." {
		t.Fatalf("Steps[0].Response.ReasoningText() = %q", got)
	}
	if got := steps[0].Response.Text(); got != "42" {
		t.Fatalf("Steps[0].Response.Text() = %q, want 42", got)
	}
}

// TestStreamTextReasoningEndCarriesSignature covers a provider that emits a
// fully assembled ReasoningEnd (Anthropic thinking blocks, with a
// signature) — the signature must survive into the step's Response content
// so it can round-trip on a later turn.
// TestStreamTextSourceEventAccumulates covers a stream emitting
// SourceEvents: they must accumulate into TextStream.Sources(),
// Step.Sources, and the assembled step Response's SourceParts(), mirroring
// how ReasoningEnd accumulates into ReasoningText.
func TestStreamTextSourceEventAccumulates(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "The sky "},
		provider.SourceEvent{Source: provider.SourcePart{ID: "source_0", URL: "https://example.com/sky", Title: "Sky Facts"}},
		provider.TextDelta{Text: "is blue."},
		provider.SourceEvent{Source: provider.SourcePart{ID: "source_1", URL: "https://example.com/color", Title: "Color Facts"}},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "why is the sky blue"})
	if err != nil {
		t.Fatal(err)
	}
	var events []provider.SourceEvent
	for p := range s.Parts() {
		if ev, ok := p.(provider.SourceEvent); ok {
			events = append(events, ev)
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(events) != 2 {
		t.Fatalf("streamed SourceEvent count = %d, want 2", len(events))
	}

	sources := s.Sources()
	if len(sources) != 2 || sources[0].ID != "source_0" || sources[1].ID != "source_1" {
		t.Fatalf("Sources() = %#v", sources)
	}
	if s.Text() != "The sky is blue." {
		t.Fatalf("Text() = %q", s.Text())
	}

	steps := s.Steps()
	if len(steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(steps))
	}
	if len(steps[0].Sources) != 2 || steps[0].Sources[0].Title != "Sky Facts" {
		t.Fatalf("Steps[0].Sources = %#v", steps[0].Sources)
	}
	if got := steps[0].Response.SourceParts(); len(got) != 2 {
		t.Fatalf("Steps[0].Response.SourceParts() = %#v", got)
	}
}

func TestStreamTextReasoningEndCarriesSignature(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ReasoningDelta{Text: "thinking"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "thinking", Signature: "sig-1"}},
		provider.TextDelta{Text: "answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	steps := s.Steps()
	if len(steps) != 1 || steps[0].Response == nil {
		t.Fatalf("Steps = %#v", steps)
	}
	var found bool
	for _, part := range steps[0].Response.Content {
		if rp, ok := part.(provider.ReasoningPart); ok {
			found = true
			if rp.Signature != "sig-1" {
				t.Fatalf("ReasoningPart.Signature = %q, want sig-1", rp.Signature)
			}
		}
	}
	if !found {
		t.Fatal("no ReasoningPart found in Steps[0].Response.Content")
	}

	// The assembled assistant message appended to Messages() must also
	// carry the reasoning part first (Anthropic wire round-trip ordering
	// is enforced in the provider, but the ai layer must at least preserve
	// it in the message content it hands back).
	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleAssistant {
		t.Fatalf("last message role = %q, want assistant", last.Role)
	}
	if _, ok := last.Content[0].(provider.ReasoningPart); !ok {
		t.Fatalf("last message Content[0] = %T, want ReasoningPart first", last.Content[0])
	}
}

// TestStreamTextReasoningUncoveredTail covers a stream with two reasoning
// spans where only the first is properly closed with a ReasoningEnd: after
// [ReasoningDelta "a", ReasoningEnd({"a"})], a second span starts
// ([ReasoningDelta "b"]) but is never closed before FinishPart. The
// resulting step must contain both the "a" ReasoningPart (from
// ReasoningEnd) and a synthesized "b" ReasoningPart for the uncovered
// trailing delta — neither is silently dropped — and ReasoningText() must
// concatenate them as "ab".
func TestStreamTextReasoningUncoveredTail(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ReasoningDelta{Text: "a"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "a"}},
		provider.ReasoningDelta{Text: "b"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	if s.ReasoningText() != "ab" {
		t.Fatalf("ReasoningText() = %q, want %q", s.ReasoningText(), "ab")
	}

	steps := s.Steps()
	if len(steps) != 1 || steps[0].Response == nil {
		t.Fatalf("Steps = %#v", steps)
	}
	var reasoningTexts []string
	for _, part := range steps[0].Response.Content {
		if rp, ok := part.(provider.ReasoningPart); ok {
			reasoningTexts = append(reasoningTexts, rp.Text)
		}
	}
	if len(reasoningTexts) != 2 || reasoningTexts[0] != "a" || reasoningTexts[1] != "b" {
		t.Fatalf("reasoning parts = %#v, want [\"a\", \"b\"]", reasoningTexts)
	}
}

// TestStreamTextReasoningTextSkipsRedacted covers a stream that emits a
// visible ReasoningEnd part alongside a Redacted one (Anthropic
// redacted_thinking, whole opaque payload delivered via ReasoningEnd with no
// preceding ReasoningDelta): TextStream.ReasoningText() and
// Step.ReasoningText must exclude the redacted ciphertext, while the
// redacted part must still be present in Step.Response.Content and the
// appended assistant message for round-tripping.
func TestStreamTextReasoningTextSkipsRedacted(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ReasoningDelta{Text: "visible"},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Text: "visible"}},
		provider.ReasoningEnd{Part: provider.ReasoningPart{Redacted: true, Text: "CIPHERTEXT"}},
		provider.TextDelta{Text: "answer"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	if s.ReasoningText() != "visible" {
		t.Fatalf("ReasoningText() = %q, want %q (redacted text must be excluded)", s.ReasoningText(), "visible")
	}

	steps := s.Steps()
	if len(steps) != 1 || steps[0].Response == nil {
		t.Fatalf("Steps = %#v", steps)
	}
	if steps[0].ReasoningText != "visible" {
		t.Fatalf("Steps[0].ReasoningText = %q, want %q", steps[0].ReasoningText, "visible")
	}

	var haveRedacted bool
	for _, part := range steps[0].Response.Content {
		if rp, ok := part.(provider.ReasoningPart); ok && rp.Redacted {
			haveRedacted = true
			if rp.Text != "CIPHERTEXT" {
				t.Fatalf("redacted part Text = %q, want CIPHERTEXT", rp.Text)
			}
		}
	}
	if !haveRedacted {
		t.Fatal("redacted ReasoningPart missing from Steps[0].Response.Content")
	}

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	var haveRedactedInMsg bool
	for _, part := range last.Content {
		if rp, ok := part.(provider.ReasoningPart); ok && rp.Redacted {
			haveRedactedInMsg = true
		}
	}
	if !haveRedactedInMsg {
		t.Fatal("redacted ReasoningPart missing from appended assistant message")
	}
}

// TestStreamTextPrepareStepSwapsModelAndPersists covers StepPlan.Model in
// the StreamText loop: PrepareStep swaps to model B on step 1 (leaving
// Model unset on step 0 and step 2), and the swap must persist — model B,
// not the original model A, must also stream step 2, even though
// PrepareStep didn't set Model again on that step.
func TestStreamTextPrepareStepSwapsModelAndPersists(t *testing.T) {
	modelA := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"a"}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	modelB := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c2", Name: "t", Args: []byte(`{"city":"b"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "done"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "r", nil })

	var sawModelOnStep1 provider.LanguageModel
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: modelA, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 3,
		PrepareStep: func(stepIndex int, plan StepPlan) (StepPlan, bool) {
			if stepIndex == 1 {
				sawModelOnStep1 = plan.Model
				plan.Model = modelB
				return plan, true
			}
			return StepPlan{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if sawModelOnStep1 != modelA {
		t.Fatalf("plan.Model seen at step 1 = %v, want modelA (the model active before the swap)", sawModelOnStep1)
	}
	if len(modelA.Calls) != 1 {
		t.Fatalf("modelA.Calls = %d, want 1 (only step 0)", len(modelA.Calls))
	}
	if len(modelB.Calls) != 2 {
		t.Fatalf("modelB.Calls = %d, want 2 (steps 1 and 2 — the swap must persist)", len(modelB.Calls))
	}
	if s.Text() != "done" {
		t.Fatalf("Text() = %q, want %q", s.Text(), "done")
	}
}

func TestStreamTextToolLoop(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallDelta{ID: "c1", Name: "t", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"Ghent"}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	var finishes int
	for p := range s.Parts() {
		if _, ok := p.(provider.FinishPart); ok {
			finishes++
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if finishes != 2 {
		t.Fatalf("finishes = %d, want 2 (one per step)", finishes)
	}
	if s.Text() != "sunny" {
		t.Fatalf("text = %q", s.Text())
	}
	if len(s.Steps()) != 2 {
		t.Fatalf("steps = %d", len(s.Steps()))
	}
	if s.Steps()[0].ToolResults[0].Result != "sunny" {
		t.Fatal("tool result missing")
	}
}

func TestStreamTextOnStepFinishInvokedOncePerStep(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallDelta{ID: "c1", Name: "t", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"Ghent"}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 3}},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var finished []Step
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		OnStepFinish: func(step Step) {
			finished = append(finished, step)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(finished) != 2 {
		t.Fatalf("OnStepFinish calls = %d, want 2 (one per step, incl. final)", len(finished))
	}
	if finished[0].ToolCalls[0].Name != "t" {
		t.Fatalf("step 0 ToolCalls = %+v", finished[0].ToolCalls)
	}
	if finished[1].Text != "sunny" {
		t.Fatalf("step 1 (final) Text = %q, want %q", finished[1].Text, "sunny")
	}
	if finished[1].FinishReason != provider.FinishStop {
		t.Fatalf("step 1 FinishReason = %v", finished[1].FinishReason)
	}
	if len(finished) != len(s.Steps()) {
		t.Fatalf("OnStepFinish count %d != len(Steps()) %d", len(finished), len(s.Steps()))
	}
}

// TestStreamTextOnStepFinishNotCalledWhenAbandoned pins down the documented
// caveat on GenerateTextOpts.OnStepFinish: if the consumer stops ranging
// over Parts() before a step's iteration completes naturally (here, by
// breaking as soon as the step's FinishPart is observed), the step-finish
// bookkeeping (appending to Steps(), invoking OnStepFinish) never runs for
// that step, even though FinishPart itself was delivered to the consumer.
func TestStreamTextOnStepFinishNotCalledWhenAbandoned(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallDelta{ID: "c1", Name: "t", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"Ghent"}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 3}},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var finished []Step
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2,
		OnStepFinish: func(step Step) {
			finished = append(finished, step)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for p := range s.Parts() {
		if _, ok := p.(provider.FinishPart); ok {
			break
		}
	}
	if len(finished) != 0 {
		t.Fatalf("OnStepFinish calls = %d, want 0 (step abandoned before iteration completed)", len(finished))
	}
	if len(s.Steps()) != 0 {
		t.Fatalf("Steps() = %d, want 0 (step never appended for an abandoned iteration)", len(s.Steps()))
	}
}

func TestStreamTextEarlyBreakCloses(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"}, provider.TextDelta{Text: "b"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	s, _ := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x"})
	for range s.Parts() {
		break
	} // must not deadlock or panic
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
}

// TestStreamTextToolLoopDeltaOnlyToolCall verifies that when a provider
// streams a tool call purely as ToolCallDelta parts (name arriving once on
// the first delta, args streamed across further deltas) without ever
// emitting a ToolCallEnd, the assembled fallback ToolCallPart still carries
// the tool Name so the registered tool is found and executed.
func TestStreamTextToolLoopDeltaOnlyToolCall(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallDelta{ID: "c1", Name: "t", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"Ghent"}`},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Tools: []Tool{tool}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(s.Steps()) != 2 {
		t.Fatalf("steps = %d, want 2", len(s.Steps()))
	}
	if len(s.Steps()[0].ToolResults) != 1 || s.Steps()[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("tool result missing or wrong: %+v", s.Steps()[0].ToolResults)
	}
	if s.Steps()[0].ToolResults[0].Err != nil {
		t.Fatalf("unexpected tool error: %v", s.Steps()[0].ToolResults[0].Err)
	}
}

// TestStreamTextMidStreamErrorSurfaces verifies that an error surfaced by the
// provider stream mid-iteration (via StreamResponse.Err()) is exposed through
// TextStream.Err() and stops the loop cleanly without retrying.
func TestStreamTextMidStreamErrorSurfaces(t *testing.T) {
	wantErr := errors.New("boom mid-stream")
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	// Wrap the mock to inject a mid-stream error via a custom StreamResponse.
	errModel := &errStreamModel{inner: m, err: wantErr}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: errModel, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for p := range s.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if got != "a" {
		t.Fatalf("got %q, want %q", got, "a")
	}
	if !errors.Is(s.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", s.Err(), wantErr)
	}
}

// errStreamModel wraps a provider.LanguageModel, returning a StreamResponse
// whose Err() reports a fixed error after replaying its parts (simulating a
// mid-stream failure that is not retried).
type errStreamModel struct {
	inner provider.LanguageModel
	err   error
}

func (m *errStreamModel) ModelID() string                     { return m.inner.ModelID() }
func (m *errStreamModel) ProviderName() string                { return m.inner.ProviderName() }
func (m *errStreamModel) Capabilities() provider.Capabilities { return m.inner.Capabilities() }
func (m *errStreamModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.inner.Generate(ctx, call)
}

func (m *errStreamModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.inner.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	return &errStreamResponse{inner: inner, err: m.err}, nil
}

type errStreamResponse struct {
	inner provider.StreamResponse
	err   error
}

func (s *errStreamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for p := range s.inner.Parts() {
			if !yield(p) {
				return
			}
		}
	}
}

func (s *errStreamResponse) Err() error   { return s.err }
func (s *errStreamResponse) Close() error { return s.inner.Close() }

// TestStreamTextMockModelStreamErrSurfaces verifies that MockModel's
// StreamErrs knob (a mid-stream error scripted parallel to Streams) is
// surfaced through TextStream.Err() and stops iteration without retrying —
// exercising the aitest fixture itself, complementing
// TestStreamTextMidStreamErrorSurfaces above which uses a hand-rolled wrapper.
func TestStreamTextMockModelStreamErrSurfaces(t *testing.T) {
	wantErr := errors.New("connection reset mid-stream")
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "par"},
			provider.TextDelta{Text: "tial"},
		}},
		StreamErrs: []error{wantErr},
	}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for p := range s.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if got != "partial" {
		t.Fatalf("got %q, want %q", got, "partial")
	}
	if !errors.Is(s.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", s.Err(), wantErr)
	}
	// No FinishPart was ever emitted, so no step should have been recorded
	// for the failed call — the loop stopped rather than fabricating one.
	if len(s.Steps()) != 0 {
		t.Fatalf("Steps() = %+v, want none", s.Steps())
	}
}

// closeRecordingModel wraps a provider.LanguageModel, returning
// StreamResponses whose Close() calls are counted, so tests can assert a
// stream's underlying connection was actually released.
type closeRecordingModel struct {
	inner       provider.LanguageModel
	closeCounts []*int
}

func (m *closeRecordingModel) ModelID() string                     { return m.inner.ModelID() }
func (m *closeRecordingModel) ProviderName() string                { return m.inner.ProviderName() }
func (m *closeRecordingModel) Capabilities() provider.Capabilities { return m.inner.Capabilities() }
func (m *closeRecordingModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.inner.Generate(ctx, call)
}

func (m *closeRecordingModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.inner.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	count := new(int)
	m.closeCounts = append(m.closeCounts, count)
	return &closeRecordingStream{inner: inner, count: count}, nil
}

type closeRecordingStream struct {
	inner provider.StreamResponse
	count *int
}

func (s *closeRecordingStream) Parts() iter.Seq[provider.StreamPart] { return s.inner.Parts() }
func (s *closeRecordingStream) Err() error                           { return s.inner.Err() }
func (s *closeRecordingStream) Close() error {
	*s.count++
	return s.inner.Close()
}

// TestTextStreamCloseNeverIterated verifies that calling Close() on a
// TextStream whose Parts() was never ranged over still releases the
// underlying provider.StreamResponse (e.g. an HTTP response body), so a
// caller who obtains a stream and decides not to consume it doesn't leak
// the connection.
func TestTextStreamCloseNeverIterated(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hello"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	rm := &closeRecordingModel{inner: m}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: rm, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.closeCounts) != 1 || *rm.closeCounts[0] != 0 {
		t.Fatalf("underlying stream closed before Close(): %v", rm.closeCounts)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("underlying stream close count = %d, want 1", *rm.closeCounts[0])
	}
	// Idempotent: calling Close() again must not double-close.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("underlying stream close count after second Close() = %d, want 1", *rm.closeCounts[0])
	}
}

// TestTextStreamCloseAfterFullIterationIsNoop verifies that Close() after
// Parts() has been fully iterated (which already closes the underlying
// stream internally) does not double-close it.
func TestTextStreamCloseAfterFullIterationIsNoop(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hello"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	rm := &closeRecordingModel{inner: m}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: rm, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("close count after full iteration = %d, want 1", *rm.closeCounts[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if *rm.closeCounts[0] != 1 {
		t.Fatalf("close count after Close() following full iteration = %d, want 1 (no double-close)", *rm.closeCounts[0])
	}
}

// TestTextStreamMessagesAfterToolLoop verifies that TextStream.Messages()
// exposes the accumulated conversation — including the assistant tool-call
// message and the tool result message — after a tool-loop stream finishes,
// matching GenerateTextResult.Messages' semantics and fixing the
// continue-the-conversation asymmetry between GenerateText and StreamText.
func TestTextStreamMessagesAfterToolLoop(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallDelta{ID: "c1", Name: "t", ArgsDelta: `{"city":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"Ghent"}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	tool := NewTool("t", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "weather?", Tools: []Tool{tool}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	msgs := s.Messages()
	// user prompt, assistant tool-call, tool result, assistant final text.
	if len(msgs) != 4 {
		t.Fatalf("Messages() = %d messages, want 4: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != provider.RoleUser {
		t.Fatalf("msgs[0].Role = %v, want user", msgs[0].Role)
	}
	if msgs[1].Role != provider.RoleAssistant {
		t.Fatalf("msgs[1].Role = %v, want assistant", msgs[1].Role)
	}
	foundToolCall := false
	for _, p := range msgs[1].Content {
		if tc, ok := p.(provider.ToolCallPart); ok && tc.ID == "c1" {
			foundToolCall = true
		}
	}
	if !foundToolCall {
		t.Fatalf("msgs[1] (assistant) missing tool call: %+v", msgs[1])
	}
	if msgs[2].Role != provider.RoleTool {
		t.Fatalf("msgs[2].Role = %v, want tool", msgs[2].Role)
	}
	foundToolResult := false
	for _, p := range msgs[2].Content {
		if tr, ok := p.(provider.ToolResultPart); ok && tr.ToolCallID == "c1" {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("msgs[2] (tool) missing tool result: %+v", msgs[2])
	}
	if msgs[3].Role != provider.RoleAssistant {
		t.Fatalf("msgs[3].Role = %v, want assistant", msgs[3].Role)
	}
}

// TestStreamTextOnChunkSeesEveryPartInOrder verifies OnChunk is invoked once
// per StreamPart, in the exact order they are produced, and strictly before
// each part is yielded to the Parts() consumer (not just eventually, and not
// batched at the end).
func TestStreamTextOnChunkSeesEveryPartInOrder(t *testing.T) {
	scripted := []provider.StreamPart{
		provider.TextDelta{Text: "hel"},
		provider.ReasoningDelta{Text: "thinking"},
		provider.TextDelta{Text: "lo"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{scripted}}

	var chunks []provider.StreamPart
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:  m,
		Prompt: "hi",
		OnChunk: func(p provider.StreamPart) {
			chunks = append(chunks, p)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	i := 0
	var yielded []provider.StreamPart
	for p := range s.Parts() {
		i++
		// OnChunk for this part must already have fired by the time the
		// consumer observes it.
		if len(chunks) != i {
			t.Fatalf("at yielded part %d, len(chunks) = %d, want %d (OnChunk must fire before yield)", i, len(chunks), i)
		}
		yielded = append(yielded, p)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	if !reflect.DeepEqual(chunks, scripted) {
		t.Fatalf("OnChunk saw:\n%#v\nwant:\n%#v", chunks, scripted)
	}
	if !reflect.DeepEqual(yielded, scripted) {
		t.Fatalf("yielded:\n%#v\nwant:\n%#v", yielded, scripted)
	}
}

// TestOnFinishEquivalenceGenerateTextVsStreamText scripts the same logical
// result (same final text/usage/finish reason, plus reasoning, sources, and
// a tool call/result on the final step) through GenerateText's
// Response-based mock path and StreamText's part-based mock path, and
// verifies OnFinish delivers equivalent *GenerateTextResult values from
// both — confirming StreamText's accumulated-state result assembly matches
// GenerateText's for the same underlying model behavior, across every field
// OnFinish exposes (not just Text/FinishReason/Usage/Messages).
func TestOnFinishEquivalenceGenerateTextVsStreamText(t *testing.T) {
	wantUsage := provider.Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}
	tool := NewTool("t", "", func(_ context.Context, _ struct{}) (any, error) { return "tool-out", nil })

	genModel := &aitest.MockModel{Responses: []*provider.Response{{
		Content: []provider.ContentPart{
			provider.ReasoningPart{Text: "because"},
			provider.TextPart{Text: "hello world"},
			provider.SourcePart{ID: "s1", URL: "https://example.com", Title: "Example"},
			provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{}`)},
		},
		FinishReason: provider.FinishToolCalls,
		Usage:        wantUsage,
	}}}
	streamModel := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hello "},
		provider.TextDelta{Text: "world"},
		provider.ReasoningDelta{Text: "because"},
		provider.SourceEvent{Source: provider.SourcePart{ID: "s1", URL: "https://example.com", Title: "Example"}},
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls, Usage: wantUsage},
	}}}

	var genResult *GenerateTextResult
	if _, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:  genModel,
		Prompt: "hi",
		Tools:  []Tool{tool},
		OnFinish: func(r *GenerateTextResult) {
			genResult = r
		},
	}); err != nil {
		t.Fatal(err)
	}

	var streamResult *GenerateTextResult
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:  streamModel,
		Prompt: "hi",
		Tools:  []Tool{tool},
		OnFinish: func(r *GenerateTextResult) {
			streamResult = r
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}

	if genResult == nil {
		t.Fatal("GenerateText: OnFinish was not called")
	}
	if streamResult == nil {
		t.Fatal("StreamText: OnFinish was not called")
	}

	if genResult.Text != streamResult.Text {
		t.Errorf("Text: GenerateText=%q StreamText=%q", genResult.Text, streamResult.Text)
	}
	if genResult.FinishReason != streamResult.FinishReason {
		t.Errorf("FinishReason: GenerateText=%v StreamText=%v", genResult.FinishReason, streamResult.FinishReason)
	}
	if genResult.Usage != streamResult.Usage {
		t.Errorf("Usage: GenerateText=%+v StreamText=%+v", genResult.Usage, streamResult.Usage)
	}
	if len(genResult.Steps) != len(streamResult.Steps) {
		t.Errorf("len(Steps): GenerateText=%d StreamText=%d", len(genResult.Steps), len(streamResult.Steps))
	}
	if !reflect.DeepEqual(genResult.Messages, streamResult.Messages) {
		t.Errorf("Messages differ:\nGenerateText=%#v\nStreamText=%#v", genResult.Messages, streamResult.Messages)
	}

	// The fields beyond Text/FinishReason/Usage/Messages that OnFinish
	// exposes must also be equivalent: reasoning, sources, and the tool
	// call/result recorded on the final step.
	if genResult.ReasoningText != streamResult.ReasoningText {
		t.Errorf("ReasoningText: GenerateText=%q StreamText=%q", genResult.ReasoningText, streamResult.ReasoningText)
	}
	if !reflect.DeepEqual(genResult.Sources, streamResult.Sources) {
		t.Errorf("Sources differ:\nGenerateText=%#v\nStreamText=%#v", genResult.Sources, streamResult.Sources)
	}
	if !reflect.DeepEqual(genResult.ToolCalls, streamResult.ToolCalls) {
		t.Errorf("ToolCalls differ:\nGenerateText=%#v\nStreamText=%#v", genResult.ToolCalls, streamResult.ToolCalls)
	}
	if len(genResult.ToolResults) != 1 || len(streamResult.ToolResults) != 1 {
		t.Fatalf("ToolResults: GenerateText=%d StreamText=%d, want 1 each", len(genResult.ToolResults), len(streamResult.ToolResults))
	}
	if genResult.ToolResults[0].Result != streamResult.ToolResults[0].Result {
		t.Errorf("ToolResults[0].Result: GenerateText=%v StreamText=%v", genResult.ToolResults[0].Result, streamResult.ToolResults[0].Result)
	}
	if genResult.ToolResults[0].Name != streamResult.ToolResults[0].Name || genResult.ToolResults[0].ToolCallID != streamResult.ToolResults[0].ToolCallID {
		t.Errorf("ToolResults[0] identity differs: GenerateText=%+v StreamText=%+v", genResult.ToolResults[0], streamResult.ToolResults[0])
	}

	// StreamText's OnFinish result must also match the TextStream's own
	// accumulated-state accessors.
	if streamResult.Text != s.Text() {
		t.Errorf("streamResult.Text = %q, s.Text() = %q", streamResult.Text, s.Text())
	}
	if streamResult.Usage != s.Usage() {
		t.Errorf("streamResult.Usage = %+v, s.Usage() = %+v", streamResult.Usage, s.Usage())
	}
	if streamResult.FinishReason != s.FinishReason() {
		t.Errorf("streamResult.FinishReason = %v, s.FinishReason() = %v", streamResult.FinishReason, s.FinishReason())
	}
}

// TestStreamTextOnFinishNotCalledOnError verifies OnFinish is skipped (in
// favor of OnError) when the stream ends abnormally.
func TestStreamTextOnFinishNotCalledOnError(t *testing.T) {
	wantErr := errors.New("boom mid-stream")
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	errModel := &errStreamModel{inner: m, err: wantErr}

	var gotErr error
	onFinishCalled := false
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:  errModel,
		Prompt: "x",
		OnError: func(e error) {
			gotErr = e
		},
		OnFinish: func(r *GenerateTextResult) {
			onFinishCalled = true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("OnError err = %v, want %v", gotErr, wantErr)
	}
	if !errors.Is(s.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", s.Err(), wantErr)
	}
	if onFinishCalled {
		t.Fatal("OnFinish must not be called when the stream ends in error")
	}
}

// TestStreamTextOnErrorToolLoopError verifies OnError also fires for a
// tool-loop error (an unknown tool requested by the model), not just for
// mid-stream provider errors.
func TestStreamTextOnErrorToolLoopError(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "unknown_tool", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}

	var gotErr error
	onFinishCalled := false
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:  m,
		Prompt: "hi",
		OnError: func(e error) {
			gotErr = e
		},
		OnFinish: func(r *GenerateTextResult) {
			onFinishCalled = true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}

	var noSuchTool *NoSuchToolError
	if !errors.As(gotErr, &noSuchTool) {
		t.Fatalf("OnError err = %v (%T), want *NoSuchToolError", gotErr, gotErr)
	}
	if !errors.As(s.Err(), &noSuchTool) {
		t.Fatalf("Err() = %v, want *NoSuchToolError", s.Err())
	}
	if onFinishCalled {
		t.Fatal("OnFinish must not be called when the tool loop errors")
	}
}

// TestStreamTextActiveToolsFiltersOfferedToolDefs mirrors
// TestActiveToolsFiltersOfferedToolDefs for StreamText: ActiveTools limits
// which tools are OFFERED (ToolDefs in the Call) while execution against an
// active tool proceeds normally.
func TestStreamTextActiveToolsFiltersOfferedToolDefs(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	other := NewTool("get_time", "", func(_ context.Context, a weatherArgs) (any, error) { return "noon", nil })
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather, other},
		ActiveTools: []string{"get_weather"}, MaxSteps: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(m.Calls[0].Tools) != 1 || m.Calls[0].Tools[0].Name != "get_weather" {
		t.Fatalf("offered tools = %+v, want only get_weather", m.Calls[0].Tools)
	}
	if s.Steps()[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("active tool execution failed: %+v", s.Steps()[0].ToolResults)
	}
}

// TestStreamTextActiveToolsInactiveCallIsNoSuchTool mirrors
// TestActiveToolsInactiveCallIsNoSuchTool for StreamText.
func TestStreamTextActiveToolsInactiveCallIsNoSuchTool(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_time", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	other := NewTool("get_time", "", func(_ context.Context, a weatherArgs) (any, error) { return "noon", nil })
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather, other},
		ActiveTools: []string{"get_weather"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	var nst *NoSuchToolError
	if !errors.As(s.Err(), &nst) || nst.ToolName != "get_time" {
		t.Fatalf("Err() = %v, want NoSuchToolError(get_time)", s.Err())
	}
}

// TestStreamTextRepairToolCallFixesUnknownName mirrors
// TestRepairToolCallFixesUnknownName for StreamText.
func TestStreamTextRepairToolCallFixesUnknownName(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_wether", Args: []byte(`{"city":"Ghent"}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			var nst *NoSuchToolError
			if errors.As(toolErr, &nst) && call.Name == "get_wether" {
				call.Name = "get_weather"
				return call, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
	if s.Steps()[0].ToolResults[0].Result != "sunny" {
		t.Fatalf("repaired call did not execute: %+v", s.Steps()[0].ToolResults)
	}
}

// TestStreamTextRepairToolCallFixesBadArgs mirrors
// TestRepairToolCallFixesBadArgs for StreamText.
func TestStreamTextRepairToolCallFixesBadArgs(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{
		{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "get_weather", Args: []byte(`{"bogus":1}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		},
		{
			provider.TextDelta{Text: "sunny"},
			provider.FinishPart{Reason: provider.FinishStop},
		},
	}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny in " + a.City, nil })
	var repairCalls int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "weather?", Tools: []Tool{weather}, MaxSteps: 2,
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			var iae *InvalidToolArgumentsError
			if errors.As(toolErr, &iae) {
				call.Args = []byte(`{"city":"Ghent"}`)
				return call, true
			}
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
	if s.Steps()[0].ToolResults[0].Err != nil {
		t.Fatalf("tool result err = %v, want nil after repair", s.Steps()[0].ToolResults[0].Err)
	}
	if s.Steps()[0].ToolResults[0].Result != "sunny in Ghent" {
		t.Fatalf("result = %v", s.Steps()[0].ToolResults[0].Result)
	}
}

// TestStreamTextRepairToolCallFalseKeepsOriginalError mirrors
// TestRepairToolCallFalseKeepsOriginalError for StreamText.
func TestStreamTextRepairToolCallFalseKeepsOriginalError(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "nope", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather},
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			return ToolCallRecord{}, false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	var nst *NoSuchToolError
	if !errors.As(s.Err(), &nst) || nst.ToolName != "nope" {
		t.Fatalf("Err() = %v, want NoSuchToolError(nope)", s.Err())
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1", repairCalls)
	}
}

// TestStreamTextRepairToolCallSingleShotCap mirrors
// TestRepairToolCallSingleShotCap for StreamText: a repaired call that fails
// again must not re-invoke RepairToolCall.
func TestStreamTextRepairToolCallSingleShotCap(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "nope", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	weather := NewTool("get_weather", "", func(_ context.Context, a weatherArgs) (any, error) { return "sunny", nil })
	var repairCalls int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x", Tools: []Tool{weather},
		RepairToolCall: func(_ context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool) {
			repairCalls++
			call.Name = "still_unknown"
			return call, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	var nst *NoSuchToolError
	if !errors.As(s.Err(), &nst) || nst.ToolName != "still_unknown" {
		t.Fatalf("Err() = %v, want NoSuchToolError(still_unknown)", s.Err())
	}
	if repairCalls != 1 {
		t.Fatalf("repairCalls = %d, want 1 (no re-invocation on second failure)", repairCalls)
	}
}

// ---------------------------------------------------------------------
// OnAbort
// ---------------------------------------------------------------------

// TestStreamTextOnAbortFiresOnEarlyBreak verifies OnAbort fires exactly once
// when the consumer abandons Parts() early (breaks before natural end), and
// that OnError/OnFinish do NOT fire for that same event.
func TestStreamTextOnAbortFiresOnEarlyBreak(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"}, provider.TextDelta{Text: "b"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	var aborted, errored, finished int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x",
		OnAbort:  func() { aborted++ },
		OnError:  func(error) { errored++ },
		OnFinish: func(*GenerateTextResult) { finished++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
		break
	}
	if aborted != 1 {
		t.Fatalf("OnAbort calls = %d, want 1", aborted)
	}
	if errored != 0 {
		t.Fatalf("OnError calls = %d, want 0", errored)
	}
	if finished != 0 {
		t.Fatalf("OnFinish calls = %d, want 0", finished)
	}
}

// TestStreamTextOnAbortNotFiredOnNaturalCompletion verifies OnAbort does not
// fire when Parts() is iterated to its natural end.
func TestStreamTextOnAbortNotFiredOnNaturalCompletion(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
		provider.FinishPart{Reason: provider.FinishStop},
	}}}
	var aborted, finished int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "x",
		OnAbort:  func() { aborted++ },
		OnFinish: func(*GenerateTextResult) { finished++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if aborted != 0 {
		t.Fatalf("OnAbort calls = %d, want 0 (natural completion)", aborted)
	}
	if finished != 1 {
		t.Fatalf("OnFinish calls = %d, want 1", finished)
	}
}

// TestStreamTextOnAbortFiresOnCtxCancelMidStream verifies that when the
// context passed to StreamText is canceled while a step's stream is in
// flight, that cancellation fires OnAbort (not OnError) exactly once, and
// that Err() still reports the underlying error.
func TestStreamTextOnAbortFiresOnCtxCancelMidStream(t *testing.T) {
	inner := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	ctx, cancel := context.WithCancel(t.Context())
	errModel := &errStreamModel{inner: inner, err: context.Canceled}

	var aborted, errored int
	s, err := StreamText(ctx, GenerateTextOpts{
		Model: errModel, Prompt: "x",
		OnAbort: func() { aborted++ },
		OnError: func(error) { errored++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel() // cancel before draining, so ctx.Err() is non-nil by the time stream.Err() is consulted
	for range s.Parts() {
	}
	if !errors.Is(s.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want context.Canceled", s.Err())
	}
	if aborted != 1 {
		t.Fatalf("OnAbort calls = %d, want 1", aborted)
	}
	if errored != 0 {
		t.Fatalf("OnError calls = %d, want 0 (ctx-cancel fires OnAbort only)", errored)
	}
}

// TestStreamTextOnAbortFiresOnCtxCancelDuringToolExecution verifies that
// OnAbort timing-independence extends past the step's own stream: a
// between-steps error (here, an unknown-tool error out of runToolCalls)
// fires OnAbort instead of OnError when ctx is already canceled by the time
// that error is observed, exactly like a mid-stream error would.
func TestStreamTextOnAbortFiresOnCtxCancelDuringToolExecution(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "unknown_tool", Args: []byte(`{}`)}},
		provider.FinishPart{Reason: provider.FinishToolCalls},
	}}}
	ctx, cancel := context.WithCancel(t.Context())

	var aborted, errored int
	s, err := StreamText(ctx, GenerateTextOpts{
		Model: m, Prompt: "hi",
		OnAbort: func() { aborted++ },
		OnError: func(error) { errored++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel() // cancel before draining, so ctx.Err() is non-nil once runToolCalls's error is observed
	for range s.Parts() {
	}

	var nst *NoSuchToolError
	if !errors.As(s.Err(), &nst) {
		t.Fatalf("Err() = %v, want *NoSuchToolError", s.Err())
	}
	if aborted != 1 {
		t.Fatalf("OnAbort calls = %d, want 1", aborted)
	}
	if errored != 0 {
		t.Fatalf("OnError calls = %d, want 0 (ctx-cancel fires OnAbort only, even for a between-steps error)", errored)
	}
}

// TestStreamTextOnAbortNotFiredOnOrdinaryMidStreamError verifies a genuine
// (non-ctx-cancel) mid-stream error fires OnError only, not OnAbort.
func TestStreamTextOnAbortNotFiredOnOrdinaryMidStreamError(t *testing.T) {
	wantErr := errors.New("boom mid-stream")
	inner := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "a"},
	}}}
	errModel := &errStreamModel{inner: inner, err: wantErr}

	var aborted, errored int
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model: errModel, Prompt: "x",
		OnAbort: func() { aborted++ },
		OnError: func(error) { errored++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if !errors.Is(s.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", s.Err(), wantErr)
	}
	if aborted != 0 {
		t.Fatalf("OnAbort calls = %d, want 0 (ordinary error fires OnError only)", aborted)
	}
	if errored != 1 {
		t.Fatalf("OnError calls = %d, want 1", errored)
	}
}
