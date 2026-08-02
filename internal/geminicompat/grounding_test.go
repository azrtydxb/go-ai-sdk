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
