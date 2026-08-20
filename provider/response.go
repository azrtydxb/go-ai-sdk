package provider

import (
	"encoding/json"
	"strings"
)

// FinishReason is why the model stopped generating, normalized across
// providers.
type FinishReason string

// The normalized finish reasons. Every provider's native reason maps onto
// one of these in its wire.go; FinishOther is the honest answer for a
// reason this SDK does not model, and is never used to paper over a
// reason it does.
const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool-calls"
	FinishContentFilter FinishReason = "content-filter"
	FinishError         FinishReason = "error"
	FinishOther         FinishReason = "other"
)

// Usage is the token accounting for one request. InputTokens and
// OutputTokens are reported by every provider; the fields below them are
// populated only where the provider breaks them out, and a zero there means
// "not reported", not "none".
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int

	// CachedInputTokens is the portion of InputTokens served from a
	// provider-side prompt cache (Anthropic cache_read_input_tokens,
	// OpenAI-compatible usage.prompt_tokens_details.cached_tokens). Zero
	// when the provider doesn't report it or no cache was used.
	CachedInputTokens int
	// ReasoningTokens is the portion of OutputTokens spent on reasoning/
	// thinking content (OpenAI-compatible
	// usage.completion_tokens_details.reasoning_tokens). Zero when the
	// provider doesn't report it.
	ReasoningTokens int
}

// Response is a completed, non-streaming model response. Content holds the
// assembled parts in order — text, reasoning, tool calls, sources — and the
// accessors below pull out one kind at a time. Raw keeps the provider's
// original body so a caller can reach anything this SDK did not model.
type Response struct {
	Content      []ContentPart
	FinishReason FinishReason
	Usage        Usage
	Raw          json.RawMessage // raw provider response body

	// ProviderMetadata carries provider-specific response data that has no
	// home in the fields above, namespaced by provider name — the same
	// convention as Call.ProviderOptions (e.g.
	// ProviderMetadata["anthropic"], ProviderMetadata["openai"]). Each
	// provider decides what (if anything) to populate under its own key;
	// nil when the provider has nothing to report.
	ProviderMetadata map[string]any
}

// Text concatenates all TextParts in the response.
func (r *Response) Text() string {
	var sb strings.Builder
	for _, part := range r.Content {
		if tp, ok := part.(TextPart); ok {
			sb.WriteString(tp.Text)
		}
	}
	return sb.String()
}

// ReasoningText concatenates all non-Redacted ReasoningParts in the
// response. Redacted parts are skipped: their Text holds opaque
// provider-encrypted data, not readable reasoning, so including it here
// would leak ciphertext into a user-facing text accessor. Redacted parts
// remain present in Content (and thus round-trip correctly back to the
// provider) — only this aggregation filters them out.
func (r *Response) ReasoningText() string {
	var sb strings.Builder
	for _, part := range r.Content {
		if rp, ok := part.(ReasoningPart); ok && !rp.Redacted {
			sb.WriteString(rp.Text)
		}
	}
	return sb.String()
}

// ToolCalls returns all ToolCallParts in the response.
func (r *Response) ToolCalls() []ToolCallPart {
	var calls []ToolCallPart
	for _, part := range r.Content {
		if tcp, ok := part.(ToolCallPart); ok {
			calls = append(calls, tcp)
		}
	}
	return calls
}

// SourceParts returns all SourceParts in the response.
func (r *Response) SourceParts() []SourcePart {
	var sources []SourcePart
	for _, part := range r.Content {
		if sp, ok := part.(SourcePart); ok {
			sources = append(sources, sp)
		}
	}
	return sources
}
