package google

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/geminicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("gemini-test")
	providertest.Run(t, providertest.Config{Model: model, ProviderName: "google"})
}

func TestCapabilities(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")
	if caps := model.Capabilities(); !caps.NativeJSON {
		t.Errorf("Capabilities().NativeJSON = false, want true")
	}
}

func TestGenerateToolResponseUsage(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	resp, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("tool")},
		Tools:    []provider.ToolDef{{Name: "get_weather", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Usage.TotalTokens != resp.Usage.InputTokens+resp.Usage.OutputTokens {
		t.Errorf("TotalTokens = %d, want sum of input+output", resp.Usage.TotalTokens)
	}
}

func TestStreamClosedTwiceIsIdempotent(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream simple")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range sr.Parts() {
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #1 = %v", err)
	}
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() #2 = %v, want nil (idempotent)", err)
	}
}

func TestStreamPartsIsSingleUse(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	sr, err := model.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("stream simple")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	var first, second int
	for range sr.Parts() {
		first++
	}
	for range sr.Parts() {
		second++
	}
	if first == 0 {
		t.Fatalf("first Parts() iteration yielded 0 parts")
	}
	if second != 0 {
		t.Errorf("second Parts() iteration yielded %d parts, want 0 (single-use)", second)
	}
}

func TestGenerateContextCancellation(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "google")
	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("gemini-test")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := model.Generate(ctx, provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
	}
}
