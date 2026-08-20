package provider

import "iter"

// StreamPart is one event from a streaming response. Like ContentPart the
// interface is closed, so a type switch over the parts below is exhaustive.
// Deltas accumulate (TextDelta, ReasoningDelta, ToolCallDelta); the End and
// Event parts arrive complete.
type StreamPart interface{ isStreamPart() }

// TextDelta is a fragment of assistant text. Fragment boundaries are the
// provider's, not the SDK's, and carry no meaning — concatenate them.
type TextDelta struct{ Text string }

func (TextDelta) isStreamPart() {}

// ToolCallDelta is a fragment of one tool call's arguments, keyed by ID.
// Several tool calls may interleave in a single stream, so accumulate
// ArgsDelta per ID rather than in arrival order; the assembled call arrives
// as ToolCallEnd.
type ToolCallDelta struct {
	ID string
	// Name may be repeated on every fragment for a given ID; consumers
	// must treat repeats as idempotent.
	Name      string
	ArgsDelta string
}

func (ToolCallDelta) isStreamPart() {}

// ToolCallEnd carries a fully assembled tool call once the provider stream
// finishes emitting its arguments — so a consumer that only wants complete
// calls can ignore ToolCallDelta entirely.
type ToolCallEnd struct{ Call ToolCallPart }

func (ToolCallEnd) isStreamPart() {}

// ReasoningDelta is a fragment of reasoning/thinking text. Providers that
// also attach a cryptographic signature to the finished reasoning block
// (Anthropic) accumulate it internally and surface it only in the final
// assembled ReasoningPart of the step's Response — there is no dedicated
// stream part for the signature.
type ReasoningDelta struct{ Text string }

func (ReasoningDelta) isStreamPart() {}

// ReasoningEnd carries a fully assembled reasoning block once the provider
// stream finishes emitting it — the reasoning analogue of ToolCallEnd. This
// is where a signed/redacted thinking block's Signature or Redacted data
// (which never arrives piecemeal as ReasoningDelta text) is delivered.
// Providers with no such assembled shape (e.g. plain reasoning_content
// text) may omit ReasoningEnd entirely; consumers fall back to the
// ReasoningDelta-accumulated text in that case.
type ReasoningEnd struct{ Part ReasoningPart }

func (ReasoningEnd) isStreamPart() {}

// SourceEvent carries a whole SourcePart discovered mid-stream. Unlike
// TextDelta/ReasoningDelta, sources arrive complete — there is no
// incremental "SourceDelta" — so this stream part is emitted once per
// source rather than accumulated by the consumer.
type SourceEvent struct{ Source SourcePart }

func (SourceEvent) isStreamPart() {}

// FinishPart is the last part of a well-formed stream: why generation
// stopped and what it cost. A stream that ends without one ended
// abnormally; check StreamResponse.Err.
type FinishPart struct {
	Reason FinishReason
	Usage  Usage

	// ProviderMetadata carries provider-specific response data that has no
	// home in the fields above, namespaced by provider name — the streaming
	// analogue of Response.ProviderMetadata (same convention as
	// Call.ProviderOptions). nil when the provider has nothing to report.
	ProviderMetadata map[string]any
}

func (FinishPart) isStreamPart() {}

// StreamResponse is an in-flight streaming response. Parts is single-use
// and must be drained or abandoned before Close; Err reports why iteration
// stopped short, and Close releases the underlying connection whether or
// not the stream ran to completion.
type StreamResponse interface {
	Parts() iter.Seq[StreamPart] // single-use; stops early on error or ctx cancel
	Err() error                  // non-nil after Parts() ends abnormally
	Close() error                // release underlying connection; safe to call twice
}
