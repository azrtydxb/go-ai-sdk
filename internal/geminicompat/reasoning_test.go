package geminicompat

// Reasoning (Gemini "thought" parts) tests: with call.Reasoning set, Gemini
// returns thought-summary parts marked "thought":true interleaved with the
// answer text. Those must surface as provider.ReasoningPart/ReasoningDelta,
// never concatenated into the visible TextPart/TextDelta answer. White-box
// (package geminicompat) because the fixtures are built directly from the
// unexported wire types, matching the style of grounding_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestGenerateThoughtPartBecomesReasoningPart covers the non-streaming path:
// a response with a thought:true part followed by a regular answer part
// must split into a ReasoningPart and a TextPart, in that order, with the
// thought text NOT appearing in resp.Text().
func TestGenerateThoughtPartBecomesReasoningPart(t *testing.T) {
	wr := generateContentResponse{
		Candidates: []wireCandidate{{
			Content: wireContent{Parts: []wirePart{
				{Text: "Let me think about this...", Thought: true},
				{Text: "The answer is 42."},
			}},
			FinishReason: "STOP",
		}},
	}
	body, _ := json.Marshal(wr)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer srv.Close()

	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")
	resp, err := model.Generate(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("what is the answer")},
		Reasoning: &provider.ReasoningConfig{Effort: "medium"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if resp.Text() != "The answer is 42." {
		t.Errorf("Text() = %q, want %q (thought text must not leak into the answer)", resp.Text(), "The answer is 42.")
	}
	if got := resp.ReasoningText(); got != "Let me think about this..." {
		t.Errorf("ReasoningText() = %q, want %q", got, "Let me think about this...")
	}

	if len(resp.Content) != 2 {
		t.Fatalf("Content = %#v, want exactly 2 parts", resp.Content)
	}
	rp, ok := resp.Content[0].(provider.ReasoningPart)
	if !ok {
		t.Fatalf("Content[0] = %T, want provider.ReasoningPart", resp.Content[0])
	}
	if rp.Text != "Let me think about this..." || rp.Redacted {
		t.Errorf("Content[0] = %#v, want {Text: %q, Redacted: false}", rp, "Let me think about this...")
	}
	tp, ok := resp.Content[1].(provider.TextPart)
	if !ok {
		t.Fatalf("Content[1] = %T, want provider.TextPart", resp.Content[1])
	}
	if tp.Text != "The answer is 42." {
		t.Errorf("Content[1].Text = %q, want %q", tp.Text, "The answer is 42.")
	}
}

// TestStreamThoughtPartBecomesReasoningDelta covers the streaming path: a
// chunk carrying a thought:true part must yield ReasoningDelta, and a
// subsequent chunk with a plain text part must yield TextDelta — never the
// other way around.
func TestStreamThoughtPartBecomesReasoningDelta(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{
			{Text: "Thinking...", Thought: true},
		}}}}},
		{
			Candidates: []wireCandidate{{
				Content:      wireContent{Parts: []wirePart{{Text: "42"}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 4, TotalTokenCount: 7},
		},
	})
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages:  []provider.Message{provider.UserText("what is the answer")},
		Reasoning: &provider.ReasoningConfig{Effort: "medium"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var reasoningDeltas []provider.ReasoningDelta
	var textDeltas []provider.TextDelta
	for part := range sr.Parts() {
		switch p := part.(type) {
		case provider.ReasoningDelta:
			reasoningDeltas = append(reasoningDeltas, p)
		case provider.TextDelta:
			textDeltas = append(textDeltas, p)
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}

	if len(reasoningDeltas) != 1 || reasoningDeltas[0].Text != "Thinking..." {
		t.Fatalf("ReasoningDelta parts = %#v, want exactly one with Text %q", reasoningDeltas, "Thinking...")
	}
	if len(textDeltas) != 1 || textDeltas[0].Text != "42" {
		t.Fatalf("TextDelta parts = %#v, want exactly one with Text %q", textDeltas, "42")
	}
}
