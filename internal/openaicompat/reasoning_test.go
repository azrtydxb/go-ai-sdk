package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func newReasoningTestModel(t *testing.T, handler http.HandlerFunc) provider.LanguageModel {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewLanguageModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "test-model")
}

func TestGenerateReasoningContent(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []chatResponseChoice{{
				Message: chatResponseMessage{
					Content:          strPtr("42"),
					ReasoningContent: "let me think...",
				},
				FinishReason: "stop",
			}},
			Usage: wireUsage{
				PromptTokens:            10,
				CompletionTokens:        5,
				TotalTokens:             15,
				PromptTokensDetails:     &wirePromptTokensDetails{CachedTokens: 4},
				CompletionTokensDetails: &wireCompletionTokensDetails{ReasoningTokens: 3},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("think")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(resp.Content) != 2 {
		t.Fatalf("Content = %d parts, want 2: %#v", len(resp.Content), resp.Content)
	}
	rp, ok := resp.Content[0].(provider.ReasoningPart)
	if !ok || rp.Text != "let me think..." {
		t.Fatalf("Content[0] = %#v, want ReasoningPart{Text: let me think...}", resp.Content[0])
	}
	if resp.ReasoningText() != "let me think..." {
		t.Errorf("ReasoningText() = %q", resp.ReasoningText())
	}
	if resp.Text() != "42" {
		t.Errorf("Text() = %q, want 42 (reasoning must not leak into Text)", resp.Text())
	}
	if resp.Usage.CachedInputTokens != 4 {
		t.Errorf("Usage.CachedInputTokens = %d, want 4", resp.Usage.CachedInputTokens)
	}
	if resp.Usage.ReasoningTokens != 3 {
		t.Errorf("Usage.ReasoningTokens = %d, want 3", resp.Usage.ReasoningTokens)
	}
}

func strPtr(s string) *string { return &s }

func TestStreamReasoningContent(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"choices":[{"delta":{"reasoning_content":"let me "}}]}`,
			`{"choices":[{"delta":{"reasoning_content":"think..."}}]}`,
			`{"choices":[{"delta":{"content":"42"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("think")},
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
		t.Fatalf("Err() = %v", err)
	}

	if len(reasoningDeltas) != 2 || reasoningDeltas[0].Text != "let me " || reasoningDeltas[1].Text != "think..." {
		t.Fatalf("ReasoningDelta parts = %#v", reasoningDeltas)
	}
	if len(textDeltas) != 1 || textDeltas[0].Text != "42" {
		t.Fatalf("TextDelta parts = %#v", textDeltas)
	}
}

func TestStreamUsageDetails(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"choices":[{"delta":{"content":"hi"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":6},"completion_tokens_details":{"reasoning_tokens":2}}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	})

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var finish provider.FinishPart
	for part := range sr.Parts() {
		if fp, ok := part.(provider.FinishPart); ok {
			finish = fp
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	if finish.Usage.CachedInputTokens != 6 {
		t.Errorf("Usage.CachedInputTokens = %d, want 6", finish.Usage.CachedInputTokens)
	}
	if finish.Usage.ReasoningTokens != 2 {
		t.Errorf("Usage.ReasoningTokens = %d, want 2", finish.Usage.ReasoningTokens)
	}
}

// TestAssistantReasoningPartNotRoundTripped covers the documented
// DeepSeek-R1 convention: a ReasoningPart in an assistant message's history
// is silently dropped (not an error, not sent back on the wire) when
// building the next request.
func TestAssistantReasoningPartNotRoundTripped(t *testing.T) {
	var lastRaw []byte
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		var err error
		lastRaw, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		resp := chatResponse{Choices: []chatResponseChoice{{Message: chatResponseMessage{Content: strPtr("ok")}, FinishReason: "stop"}}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{
			provider.UserText("hi"),
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

	if strings.Contains(string(lastRaw), "internal reasoning") {
		t.Errorf("request body contains reasoning text, want dropped: %s", lastRaw)
	}
}
