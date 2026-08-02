package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- ProviderMetadata ----

func TestGenerateSystemFingerprintPopulatesProviderMetadata(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []chatResponseChoice{{
				Message:      chatResponseMessage{Content: strPtr("hi")},
				FinishReason: "stop",
			}},
			SystemFingerprint: "fp_abc123",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	meta, ok := resp.ProviderMetadata["test"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[test] = %#v, want map[string]any", resp.ProviderMetadata["test"])
	}
	if meta["system_fingerprint"] != "fp_abc123" {
		t.Errorf("system_fingerprint = %#v, want fp_abc123", meta["system_fingerprint"])
	}
}

func TestGenerateNoSystemFingerprintLeavesProviderMetadataNil(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		resp := chatResponse{
			Choices: []chatResponseChoice{{
				Message:      chatResponseMessage{Content: strPtr("hi")},
				FinishReason: "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

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
