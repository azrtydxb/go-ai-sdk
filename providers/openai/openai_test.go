package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "openai")
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("gpt-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "openai"})
}

// streamSSEServer starts an httptest server that writes exactly the given
// raw SSE "data:" payloads (already JSON-encoded) and then closes the
// response without a trailing "data: [DONE]" — simulating a proxy/load
// balancer that truncates the stream after forwarding real chunks.
//
// The payloads are plain JSON string literals (rather than being built from
// wire structs) because the wire types now live, unexported, in
// internal/openaicompat: this test is about streamResponse's robustness to
// a truncated connection, not about wire serialization, so it only needs
// valid chat-completions-shaped SSE data on the wire.
func streamSSEServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("fixture: ResponseWriter does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		// Deliberately no "data: [DONE]" — the handler returns here,
		// closing the response body from the client's point of view.
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestStreamEndsWithoutDoneButHasFinishReason covers the case where the
// server sends a finish_reason chunk (and usage) but the connection closes
// before a "data: [DONE]" sentinel arrives. Per the documented rule in
// streamResponse.Parts, this must still yield exactly one FinishPart with
// Err() == nil.
func TestStreamEndsWithoutDoneButHasFinishReason(t *testing.T) {
	srv := streamSSEServer(t,
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo!"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`{"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
	)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

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
		t.Fatalf("Err() = %v, want nil (finish_reason was received before truncation)", err)
	}
	if len(finishes) != 1 {
		t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
	}
	if finishes[0].Reason != provider.FinishStop {
		t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
	}
	if finishes[0].Usage.TotalTokens != 7 {
		t.Errorf("FinishPart.Usage.TotalTokens = %d, want 7", finishes[0].Usage.TotalTokens)
	}
}

// TestStreamTruncatedBeforeFinishReason covers the case where the
// connection closes before any finish_reason chunk arrives at all — a true
// mid-response truncation. This must yield zero FinishParts and a non-nil
// Err().
func TestStreamTruncatedBeforeFinishReason(t *testing.T) {
	srv := streamSSEServer(t,
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
	)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gpt-test")

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
		t.Fatal("Err() = nil, want non-nil (stream truncated before finish_reason)")
	}
	if len(finishes) != 0 {
		t.Errorf("got %d FinishPart(s), want 0: %+v", len(finishes), finishes)
	}
}
