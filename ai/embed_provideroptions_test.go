package ai

// Tests that Embed/EmbedMany thread EmbedOpts.ProviderOptions through to
// provider.EmbeddingModelWithOptions.EmbedCall when the model implements it, and
// fall back to plain Embed (silently ignoring ProviderOptions) otherwise.

import (
	"context"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// v2MockEmbedder is a provider.EmbeddingModelWithOptions test double that records
// the ProviderOptions passed to each EmbedCall.
type v2MockEmbedder struct {
	aitest.MockEmbedder
	lastProviderOptions map[string]any
	calls               int
}

func (m *v2MockEmbedder) EmbedCall(ctx context.Context, call provider.EmbeddingCall) (*provider.EmbeddingResponse, error) {
	m.calls++
	m.lastProviderOptions = call.ProviderOptions
	return m.MockEmbedder.Embed(ctx, call.Values)
}

func TestEmbedThreadsProviderOptionsToV2Model(t *testing.T) {
	m := &v2MockEmbedder{}
	opts := map[string]any{"mock": map[string]any{"k": "v"}}

	_, err := Embed(t.Context(), EmbedOpts{Model: m, Value: "a", ProviderOptions: opts})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if m.calls != 1 {
		t.Fatalf("EmbedCall calls = %d, want 1", m.calls)
	}
	if m.lastProviderOptions["mock"] == nil {
		t.Errorf("ProviderOptions not threaded through to EmbedCall: %+v", m.lastProviderOptions)
	}
}

func TestEmbedManyThreadsProviderOptionsToV2Model(t *testing.T) {
	m := &v2MockEmbedder{}
	opts := map[string]any{"mock": map[string]any{"k": "v"}}

	_, err := EmbedMany(t.Context(), EmbedManyOpts{Model: m, Values: []string{"a", "b"}, ProviderOptions: opts})
	if err != nil {
		t.Fatalf("EmbedMany: %v", err)
	}
	if m.calls == 0 {
		t.Fatalf("EmbedCall calls = %d, want > 0", m.calls)
	}
	if m.lastProviderOptions["mock"] == nil {
		t.Errorf("ProviderOptions not threaded through to EmbedCall: %+v", m.lastProviderOptions)
	}
}

func TestEmbedFallsBackToPlainEmbedForNonV2Model(t *testing.T) {
	m := &aitest.MockEmbedder{}
	opts := map[string]any{"mock": map[string]any{"k": "v"}}

	// aitest.MockEmbedder does not implement provider.EmbeddingModelWithOptions;
	// Embed must fall back to plain Embed (values still work; the
	// ProviderOptions have no effect but must not error or panic).
	res, err := Embed(t.Context(), EmbedOpts{Model: m, Value: "abc", ProviderOptions: opts})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(m.Batches) != 1 || len(m.Batches[0]) != 1 || m.Batches[0][0] != "abc" {
		t.Fatalf("Batches = %+v, want [[abc]]", m.Batches)
	}
	if len(res.Embedding) == 0 {
		t.Fatalf("Embedding empty")
	}
}
