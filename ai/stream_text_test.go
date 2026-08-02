package ai

import (
	"context"
	"errors"
	"iter"
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
