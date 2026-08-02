package provider

import (
	"encoding/json"
	"strings"
)

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool-calls"
	FinishContentFilter FinishReason = "content-filter"
	FinishError         FinishReason = "error"
	FinishOther         FinishReason = "other"
)

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

// Text concatenates all TextParts in the response
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

// ToolCalls returns all ToolCallParts in the response
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
