package geminicompat

// Streaming edge-case tests that construct raw wire response chunks
// directly. These are white-box (package geminicompat) because they build
// geminicompat's unexported wire response types by hand to simulate a
// truncated connection — moved verbatim from the google package's former
// google_test.go, only the model construction changed from
// google.New(...).Model(...) to NewLanguageModel(Config{...}, ...).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// streamSSEServer starts an httptest server that writes exactly the given
// raw response chunks as SSE "data:" events and then closes the response —
// simulating a proxy/load balancer that truncates the stream.
func streamSSEServer(t *testing.T, chunks []generateContentResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			writeSSE(w, flusher, c)
		}
		// Deliberately no closing marker — the handler returns here, closing
		// the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, v any) {
	b, _ := json.Marshal(v)
	w.Write([]byte("data: "))
	w.Write(b)
	w.Write([]byte("\n\n"))
	flusher.Flush()
}

// TestStreamToolCallIDsAreDistinctAcrossChunks covers two calls to the SAME
// tool name arriving in two SEPARATE SSE chunks. The synthesized ID must be
// unique per call (a counter across the whole stream), not reset to 0 for
// each chunk's parts slice — otherwise both calls would collide on
// "call_get_weather_0", breaking ID-based tool-call/result correlation.
func TestStreamToolCallIDsAreDistinctAcrossChunks(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{
			FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Ghent"}`)},
		}}}}}},
		{
			Candidates: []wireCandidate{{
				Content: wireContent{Parts: []wirePart{{
					FunctionCall: &wireFunctionCall{Name: "get_weather", Args: json.RawMessage(`{"city":"Bruges"}`)},
				}}},
				FinishReason: "STOP",
			}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 6, CandidatesTokenCount: 4, TotalTokenCount: 10},
		},
	})
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
		Tools:    []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var ends []provider.ToolCallEnd
	for part := range sr.Parts() {
		if end, ok := part.(provider.ToolCallEnd); ok {
			ends = append(ends, end)
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(ends) != 2 {
		t.Fatalf("got %d ToolCallEnd part(s), want exactly 2: %+v", len(ends), ends)
	}
	if ends[0].Call.ID == ends[1].Call.ID {
		t.Errorf("ToolCallEnd IDs collide: both %q (must be distinct across chunks)", ends[0].Call.ID)
	}
}

// TestStreamEndsWithFinishReasonSeen covers the case where the connection
// closes right after a chunk carrying a finishReason (Gemini has no
// [DONE]/message_stop sentinel — natural EOF is the only signal). This must
// still yield exactly one FinishPart with Err() == nil.
func TestStreamEndsWithFinishReasonSeen(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "Hel"}}}}}},
		{
			Candidates:    []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "lo!"}}}, FinishReason: "STOP"}},
			UsageMetadata: &wireUsageMetadata{PromptTokenCount: 3, CandidatesTokenCount: 2, TotalTokenCount: 5},
		},
	})
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil (finishReason was received)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.TotalTokens != 5 {
		t.Errorf("FinishPart.Usage.TotalTokens = %d, want 5", finishes[0].Usage.TotalTokens)
	}
}

// TestStreamTruncatedBeforeFinishReason covers a true mid-response
// truncation: the connection closes before any chunk carries a
// finishReason. This must yield zero FinishParts and a non-nil Err().
func TestStreamTruncatedBeforeFinishReason(t *testing.T) {
	srv := streamSSEServer(t, []generateContentResponse{
		{Candidates: []wireCandidate{{Content: wireContent{Parts: []wirePart{{Text: "Hel"}}}}}},
	})
	model := NewLanguageModel(testConfig(srv.URL), "gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("anything")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finishes []provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finishes = append(finishes, fp)
		}
	}

	if err := sr.Err(); err == nil {
		t.Fatal("Err() = nil, want non-nil (stream truncated before finishReason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}
