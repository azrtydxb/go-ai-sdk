package provider

import "iter"

type StreamPart interface{ isStreamPart() }

type TextDelta struct{ Text string }

func (TextDelta) isStreamPart() {}

type ToolCallDelta struct {
	ID string
	// Name may be repeated on every fragment for a given ID; consumers
	// must treat repeats as idempotent.
	Name      string
	ArgsDelta string
}

func (ToolCallDelta) isStreamPart() {}

type ToolCallEnd struct{ Call ToolCallPart } // complete, args fully assembled

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

type StreamResponse interface {
	Parts() iter.Seq[StreamPart] // single-use; stops early on error or ctx cancel
	Err() error                  // non-nil after Parts() ends abnormally
	Close() error                // release underlying connection; safe to call twice
}
