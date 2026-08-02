package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestStreamSystemFingerprintPopulatesFinishPartProviderMetadata(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"choices":[{"delta":{"content":"hi"}}],"system_fingerprint":"fp_abc123"}`,
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

	meta, ok := finish.ProviderMetadata["test"].(map[string]any)
	if !ok {
		t.Fatalf("ProviderMetadata[test] = %#v, want map[string]any", finish.ProviderMetadata["test"])
	}
	if meta["system_fingerprint"] != "fp_abc123" {
		t.Errorf("system_fingerprint = %#v, want fp_abc123", meta["system_fingerprint"])
	}
}

func TestStreamNoSystemFingerprintLeavesFinishPartProviderMetadataNil(t *testing.T) {
	model := newReasoningTestModel(t, func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"choices":[{"delta":{"content":"hi"}}]}`,
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
