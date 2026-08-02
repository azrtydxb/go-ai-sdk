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
