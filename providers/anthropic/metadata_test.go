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
