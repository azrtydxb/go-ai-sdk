package geminicompat

// Grounding (Google Search grounding / citations) tests: candidates[0].
// groundingMetadata.groundingChunks[].web{uri,title} must surface as
// provider.SourcePart content parts (Generate) or provider.SourceEvent
// stream parts (Stream). White-box (package geminicompat) because the
// fixtures are built directly from the unexported wire types, matching the
// style of streaming_wire_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TestAssistantSourcePartSkippedNotError and
// TestAssistantReasoningPartSkippedNotError cover the spec-owner ruling
// that SDK-generated informational content parts must be replay-safe: a
// SourcePart or ReasoningPart in an assistant message's history (e.g. from
// a prior grounded turn, or a step that also produced reasoning) must be
// silently skipped when building the next request — not rejected as an
// unsupported content part, and not present on the wire.
func TestAssistantSourcePartSkippedNotError(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.TextPart{Text: "The sky is blue."},
					provider.SourcePart{ID: "source_0", URL: "https://example.com/sky", Title: "Sky Facts"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	content := req.Contents[1]
	if content.Role != "model" {
		t.Errorf("assistant content Role = %q, want model", content.Role)
	}
	if len(content.Parts) != 1 || content.Parts[0].Text != "The sky is blue." {
		t.Fatalf("Parts = %#v, want exactly one text part (SourcePart dropped)", content.Parts)
	}
}

func TestAssistantReasoningPartSkippedNotError(t *testing.T) {
	model, srv := newTestLanguageModel(t)

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("simple"),
			{
				Role: provider.RoleAssistant,
				Content: []provider.ContentPart{
					provider.ReasoningPart{Text: "internal reasoning"},
					provider.TextPart{Text: "answer"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	req := lastRequest(t, srv)
	content := req.Contents[1]
	if len(content.Parts) != 1 || content.Parts[0].Text != "answer" {
		t.Fatalf("Parts = %#v, want exactly one text part (ReasoningPart dropped)", content.Parts)
	}
}

func TestGenerateGroundingChunksBecomeSourceParts(t *testing.T) {
	wr := generateContentResponse{
		Candidates: []wireCandidate{{
			Content: wireContent{Parts: []wirePart{{Text: "The sky is blue."}}},
			GroundingMetadata: &wireGroundingMeta{GroundingChunks: []wireGroundingChunk{
				{Web: &wireGroundingWeb{URI: "https://example.com/sky", Title: "Sky Facts"}},
				{Web: &wireGroundingWeb{URI: "https://example.com/color", Title: "Color Facts"}},
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
		Messages: []provider.Message{provider.UserText("why is the sky blue")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	sources := resp.SourceParts()
	if len(sources) != 2 {
		t.Fatalf("SourceParts() len = %d, want 2: %#v", len(sources), sources)
	}
	if sources[0].ID != "source_0" || sources[0].URL != "https://example.com/sky" || sources[0].Title != "Sky Facts" {
		t.Errorf("sources[0] = %#v", sources[0])
	}
	if sources[1].ID != "source_1" || sources[1].URL != "https://example.com/color" || sources[1].Title != "Color Facts" {
		t.Errorf("sources[1] = %#v", sources[1])
	}
	if resp.Text() != "The sky is blue." {
		t.Errorf("Text() = %q", resp.Text())
	}

	// A candidate with no groundingMetadata must produce no SourceParts.
	wr2 := generateContentResponse{Candidates: []wireCandidate{{
		Content:      wireContent{Parts: []wirePart{{Text: "no grounding here"}}},
		FinishReason: "STOP",
	}}}
	resp2 := convertResponse(wr2, nil)
	if got := resp2.SourceParts(); len(got) != 0 {
		t.Errorf("SourceParts() with no groundingMetadata = %#v, want empty", got)
	}
}

func TestStreamGroundingChunksBecomeSourceEvents(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "The sky "}}}}}},
		{
			Candidates: []wireCandidate{{
				Content: wireContent{Parts: []wirePart{{Text: "is blue."}}},
				GroundingMetadata: &wireGroundingMeta{GroundingChunks: []wireGroundingChunk{
					{Web: &wireGroundingWeb{URI: "https://example.com/sky", Title: "Sky Facts"}},
				}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 4, TotalTokenCount: 7},
		},
	})
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("why is the sky blue")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var events []provider.SourceEvent
	for part := range sr.Parts() {
		if ev, ok := part.(provider.SourceEvent); ok {
			events = append(events, ev)
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d SourceEvent(s), want exactly 1: %+v", len(events), events)
	}
	if events[0].Source.ID != "source_0" || events[0].Source.URL != "https://example.com/sky" || events[0].Source.Title != "Sky Facts" {
		t.Errorf("SourceEvent.Source = %#v", events[0].Source)
	}
}
