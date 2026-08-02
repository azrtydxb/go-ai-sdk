// Package providertest is a conformance suite for provider.LanguageModel
// implementations. Provider packages (OpenAI, Anthropic, Google, ...) build
// a fixture HTTP server that speaks their wire format and dispatches on the
// text of the last user message, then call Run against a model wired to
// that fixture server.
//
// Scenario keys the fixture server must implement, keyed off the last user
// message's text content:
//
//	"simple"        -> respond with text "Hello from <provider>!" and usage
//	"tool"          -> respond with one tool call: name "get_weather",
//	                   args {"city":"Ghent"} (any ID), finish tool-calls
//	"stream simple" -> stream the text "Hello!" in >=2 chunks, then finish
//	                   with reason stop and non-zero usage
//	"stream tool"   -> stream one complete tool call (deltas then end)
//	"fail 429"      -> HTTP 429 with any body
//	"fail 400"      -> HTTP 400 with any body
package providertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// Config configures a Run of the conformance suite against a single
// provider.LanguageModel implementation.
type Config struct {
	Model        provider.LanguageModel
	ProviderName string // expected ProviderName()
}

// weatherTool is the tool definition scenario-based fixture servers can use
// to decide when to emit a tool call. Providers that require at least one
// tool definition present in the request in order to emit a tool call get
// one here.
var weatherTool = provider.ToolDef{
	Name:        "get_weather",
	Description: "Get the current weather for a city",
	Schema:      json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`),
}

// Run executes the conformance matrix as subtests against cfg.Model.
func Run(t *testing.T, cfg Config) {
	t.Helper()

	t.Run("ProviderName", func(t *testing.T) {
		if got := cfg.Model.ProviderName(); got != cfg.ProviderName {
			t.Errorf("ProviderName() = %q, want %q", got, cfg.ProviderName)
		}
	})

	t.Run("Generate/simple", func(t *testing.T) {
		resp, err := cfg.Model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("simple")},
		})
		if err != nil {
			t.Fatalf("Generate(%q): unexpected error: %v", "simple", err)
		}
		wantText := fmt.Sprintf("Hello from %s!", cfg.ProviderName)
		if got := resp.Text(); got != wantText {
			t.Errorf("Text() = %q, want %q", got, wantText)
		}
		if resp.FinishReason != provider.FinishStop {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, provider.FinishStop)
		}
		if resp.Usage.TotalTokens <= 0 {
			t.Errorf("Usage.TotalTokens = %d, want > 0", resp.Usage.TotalTokens)
		}
		if len(resp.Raw) == 0 {
			t.Errorf("Raw is empty, want the raw provider response body")
		}
	})

	t.Run("Generate/tool", func(t *testing.T) {
		resp, err := cfg.Model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("tool")},
			Tools:    []provider.ToolDef{weatherTool},
		})
		if err != nil {
			t.Fatalf("Generate(%q): unexpected error: %v", "tool", err)
		}
		calls := resp.ToolCalls()
		if len(calls) != 1 {
			t.Fatalf("ToolCalls() returned %d calls, want exactly 1: %+v", len(calls), calls)
		}
		if calls[0].Name != "get_weather" {
			t.Errorf("ToolCalls()[0].Name = %q, want %q", calls[0].Name, "get_weather")
		}
		var args struct {
			City string `json:"city"`
		}
		if err := json.Unmarshal(calls[0].Args, &args); err != nil {
			t.Fatalf("ToolCalls()[0].Args = %s: unmarshal failed: %v", calls[0].Args, err)
		}
		if args.City != "Ghent" {
			t.Errorf("ToolCalls()[0].Args city = %q, want %q", args.City, "Ghent")
		}
		if resp.FinishReason != provider.FinishToolCalls {
			t.Errorf("FinishReason = %q, want %q", resp.FinishReason, provider.FinishToolCalls)
		}
	})

	t.Run("Stream/simple", func(t *testing.T) {
		sr, err := cfg.Model.Stream(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("stream simple")},
		})
		if err != nil {
			t.Fatalf("Stream(%q): unexpected error: %v", "stream simple", err)
		}
		defer sr.Close()

		var text strings.Builder
		textDeltas := 0
		var finishes []provider.FinishPart
		for part := range sr.Parts() {
			switch p := part.(type) {
			case provider.TextDelta:
				text.WriteString(p.Text)
				textDeltas++
			case provider.FinishPart:
				finishes = append(finishes, p)
			}
		}
		if err := sr.Err(); err != nil {
			t.Fatalf("Err() = %v, want nil", err)
		}
		if textDeltas < 2 {
			t.Errorf("got %d TextDelta parts, want >= 2 (text must arrive in multiple chunks)", textDeltas)
		}
		if got := text.String(); got != "Hello!" {
			t.Errorf("concatenated text = %q, want %q", got, "Hello!")
		}
		if len(finishes) != 1 {
			t.Fatalf("got %d FinishPart(s), want exactly 1: %+v", len(finishes), finishes)
		}
		if finishes[0].Reason != provider.FinishStop {
			t.Errorf("FinishPart.Reason = %q, want %q", finishes[0].Reason, provider.FinishStop)
		}
		if finishes[0].Usage.TotalTokens <= 0 {
			t.Errorf("FinishPart.Usage.TotalTokens = %d, want > 0", finishes[0].Usage.TotalTokens)
		}
	})

	t.Run("Stream/tool", func(t *testing.T) {
		sr, err := cfg.Model.Stream(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("stream tool")},
			Tools:    []provider.ToolDef{weatherTool},
		})
		if err != nil {
			t.Fatalf("Stream(%q): unexpected error: %v", "stream tool", err)
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
		if len(ends) != 1 {
			t.Fatalf("got %d ToolCallEnd part(s), want exactly 1: %+v", len(ends), ends)
		}
		if ends[0].Call.Name != "get_weather" {
			t.Errorf("ToolCallEnd.Call.Name = %q, want %q", ends[0].Call.Name, "get_weather")
		}
		var args struct {
			City string `json:"city"`
		}
		if err := json.Unmarshal(ends[0].Call.Args, &args); err != nil {
			t.Fatalf("ToolCallEnd.Call.Args = %s: unmarshal failed: %v", ends[0].Call.Args, err)
		}
		if args.City != "Ghent" {
			t.Errorf("ToolCallEnd.Call.Args city = %q, want %q", args.City, "Ghent")
		}
	})

	t.Run("Error/429", func(t *testing.T) {
		_, err := cfg.Model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("fail 429")},
		})
		if err == nil {
			t.Fatal("Generate(\"fail 429\"): want error, got nil")
		}
		var apiErr *ai.APICallError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error is %T (%v), want *ai.APICallError (via errors.As)", err, err)
		}
		if apiErr.StatusCode != 429 {
			t.Errorf("StatusCode = %d, want 429", apiErr.StatusCode)
		}
		if !apiErr.Retryable {
			t.Errorf("Retryable = false, want true for a 429")
		}
	})

	t.Run("Error/400", func(t *testing.T) {
		_, err := cfg.Model.Generate(context.Background(), provider.Call{
			Messages: []provider.Message{provider.UserText("fail 400")},
		})
		if err == nil {
			t.Fatal("Generate(\"fail 400\"): want error, got nil")
		}
		var apiErr *ai.APICallError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error is %T (%v), want *ai.APICallError (via errors.As)", err, err)
		}
		if apiErr.StatusCode != 400 {
			t.Errorf("StatusCode = %d, want 400", apiErr.StatusCode)
		}
		if apiErr.Retryable {
			t.Errorf("Retryable = true, want false for a 400")
		}
	})

	t.Run("Cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := cfg.Model.Generate(ctx, provider.Call{
			Messages: []provider.Message{provider.UserText("simple")},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
		}
	})

	t.Run("Cancel/Stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := cfg.Model.Stream(ctx, provider.Call{
			Messages: []provider.Message{provider.UserText("stream simple")},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled (via errors.Is)", err)
		}
	})
}
