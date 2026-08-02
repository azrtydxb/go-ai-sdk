package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- ProviderMetadata ----

func TestGenerateCacheCreationPopulatesProviderMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := messageResponse{
			Content:    []wireContentBlock{{Type: "text", Text: "hi"}},
			StopReason: "end_turn",
			Usage: wireUsage{
				InputTokens:              10,
				OutputTokens:             5,
				CacheCreationInputTokens: 7,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	meta, ok := resp.ProviderMetadata["anthropic"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[anthropic] = %#v, want map[string]any", resp.ProviderMetadata["anthropic"])
	}
	got, ok := meta["cache_creation_input_tokens"].(int)
	if !ok || got != 7 {
		t.Errorf("cache_creation_input_tokens = %#v, want 7", meta["cache_creation_input_tokens"])
	}
}

func TestGenerateNoCacheCreationLeavesProviderMetadataNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := messageResponse{
			Content:    []wireContentBlock{{Type: "text", Text: "hi"}},
			StopReason: "end_turn",
			Usage:      wireUsage{InputTokens: 10, OutputTokens: 5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if resp.ProviderMetadata != nil {
		t.Errorf("ProviderMetadata = %#v, want nil", resp.ProviderMetadata)
	}
}

func TestStreamCacheCreationPopulatesFinishPartProviderMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		writeNamedSSE(w, flusher, "message_start", messageStartEvent{
			Message: struct {
				Usage *messageStartUsage `json:"usage,omitempty"`
			}{Usage: &messageStartUsage{InputTokens: 10, CacheCreationInputTokens: 7}},
		})
		writeNamedSSE(w, flusher, "content_block_start", contentBlockStartEvent{
			Index: 0, ContentBlock: streamContentBlock{Type: "text"},
		})
		writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
			Index: 0, Delta: streamDelta{Type: "text_delta", Text: "hi"},
		})
		writeNamedSSE(w, flusher, "content_block_stop", contentBlockStopEvent{Index: 0})
		md := messageDeltaEvent{Usage: &messageDeltaUsage{OutputTokens: 5}}
		md.Delta.StopReason = "end_turn"
		writeNamedSSE(w, flusher, "message_delta", md)
		writeNamedSSE(w, flusher, "message_stop", struct{}{})
	}))
	t.Cleanup(srv.Close)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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

	meta, ok := finish.ProviderMetadata["anthropic"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[anthropic] = %#v, want map[string]any", finish.ProviderMetadata["anthropic"])
	}
	if got, ok := meta["cache_creation_input_tokens"].(int); !ok || got != 7 {
		t.Errorf("cache_creation_input_tokens = %#v, want 7", meta["cache_creation_input_tokens"])
	}
}

func TestStreamNoCacheCreationLeavesFinishPartProviderMetadataNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		writeNamedSSE(w, flusher, "message_start", messageStartEvent{
			Message: struct {
				Usage *messageStartUsage `json:"usage,omitempty"`
			}{Usage: &messageStartUsage{InputTokens: 10}},
		})
		writeNamedSSE(w, flusher, "content_block_start", contentBlockStartEvent{
			Index: 0, ContentBlock: streamContentBlock{Type: "text"},
		})
		writeNamedSSE(w, flusher, "content_block_delta", contentBlockDeltaEvent{
			Index: 0, Delta: streamDelta{Type: "text_delta", Text: "hi"},
		})
		writeNamedSSE(w, flusher, "content_block_stop", contentBlockStopEvent{Index: 0})
		md := messageDeltaEvent{Usage: &messageDeltaUsage{OutputTokens: 5}}
		md.Delta.StopReason = "end_turn"
		writeNamedSSE(w, flusher, "message_delta", md)
		writeNamedSSE(w, flusher, "message_stop", struct{}{})
	}))
	t.Cleanup(srv.Close)
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")

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

	if finish.ProviderMetadata != nil {
		t.Errorf("ProviderMetadata = %#v, want nil", finish.ProviderMetadata)
	}
}
