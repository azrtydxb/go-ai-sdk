// Package ai_test holds these tests in an external test package: they
// exercise real provider packages (anthropic, openai), which import ai
// itself, so living in package ai would create an import cycle. See the
// same rationale in registry_test.go.
package ai_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

func TestStreamTextAnthropicCacheCreationPopulatesStepProviderMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":10,\"cache_creation_input_tokens\":7}}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"index\":0}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	model := anthropic.New(anthropic.WithAPIKey("k"), anthropic.WithBaseURL(srv.URL)).Model("claude-test")

	s, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	for range s.Parts() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	steps := s.Steps()
	if len(steps) == 0 {
		t.Fatal("Steps is empty")
	}
	resp := steps[len(steps)-1].Response
	meta, ok := resp.ProviderMetadata["anthropic"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[anthropic] = %#v, want map[string]any", resp.ProviderMetadata["anthropic"])
	}
	if got, ok := meta["cache_creation_input_tokens"].(int); !ok || got != 7 {
		t.Errorf("cache_creation_input_tokens = %#v, want 7", meta["cache_creation_input_tokens"])
	}
}

func TestStreamTextAnthropicNoCacheCreationLeavesStepProviderMetadataNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: content_block_stop\ndata: {\"index\":0}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	model := anthropic.New(anthropic.WithAPIKey("k"), anthropic.WithBaseURL(srv.URL)).Model("claude-test")

	s, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	for range s.Parts() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	resp := s.Steps()[len(s.Steps())-1].Response
	if resp.ProviderMetadata != nil {
		t.Errorf("ProviderMetadata = %#v, want nil", resp.ProviderMetadata)
	}
}

func TestStreamTextOpenAISystemFingerprintPopulatesStepProviderMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"system_fingerprint\":\"fp_abc123\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	model := openai.New(openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL)).Model("gpt-test")

	s, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	for range s.Parts() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	resp := s.Steps()[len(s.Steps())-1].Response
	meta, ok := resp.ProviderMetadata["openai"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[openai] = %#v, want map[string]any", resp.ProviderMetadata["openai"])
	}
	if meta["system_fingerprint"] != "fp_abc123" {
		t.Errorf("system_fingerprint = %#v, want fp_abc123", meta["system_fingerprint"])
	}
}

func TestStreamTextOpenAINoSystemFingerprintLeavesStepProviderMetadataNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(srv.Close)
	model := openai.New(openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL)).Model("gpt-test")

	s, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("StreamText: %v", err)
	}
	for range s.Parts() {
	}
	if err := s.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}

	resp := s.Steps()[len(s.Steps())-1].Response
	if resp.ProviderMetadata != nil {
		t.Errorf("ProviderMetadata = %#v, want nil", resp.ProviderMetadata)
	}
}
