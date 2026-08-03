# Streaming

`ai.StreamText` returns a `*ai.TextStream`, a single-use iterator over
`provider.StreamPart` values plus accessors for the accumulated result once
iteration ends. This page covers the part types, the iterator/`Err`/`Close`
contract, and `ai.SmoothStream`. For `StreamText`'s options (`OnChunk`,
`OnStepFinish`, etc.) see [Generating text](generating-text.md).

## StreamPart reference

`provider.StreamPart` is a closed interface (`isStreamPart()`); every
concrete type:

- **`TextDelta{Text string}`** — a fragment of assistant text.
- **`ToolCallDelta{ID, Name, ArgsDelta string}`** — a fragment of a tool
  call's arguments. `Name` may repeat on every fragment for a given `ID`;
  consumers must treat repeats as idempotent.
- **`ToolCallEnd{Call provider.ToolCallPart}`** — a complete tool call, args
  fully assembled.
- **`ReasoningDelta{Text string}`** — a fragment of reasoning/thinking text.
- **`ReasoningEnd{Part provider.ReasoningPart}`** — a fully assembled
  reasoning block, once the provider stream finishes emitting it. This is
  where a signed/redacted thinking block's `Signature`/`Redacted` data
  (which never arrives piecemeal as `ReasoningDelta` text) is delivered.
  Providers with no such assembled shape (plain `reasoning_content` text,
  e.g. DeepSeek) may omit `ReasoningEnd` entirely — see
  [Reasoning](reasoning.md).
- **`SourceEvent{Source provider.SourcePart}`** — a whole citation/grounding
  source discovered mid-stream. Unlike text/reasoning, sources arrive
  complete: there is no incremental "SourceDelta," so this part is emitted
  once per source.
- **`FinishPart{Reason, Usage, ProviderMetadata}`** — one per step,
  ending it. `ProviderMetadata` carries provider-specific response data
  namespaced by provider name (the streaming analogue of
  `Response.ProviderMetadata` — see
  [Provider options](provider-options.md)), `nil` when the provider has
  nothing to report.

```go
for part := range stream.Parts() {
	switch p := part.(type) {
	case provider.TextDelta:
		fmt.Print(p.Text)
	case provider.ReasoningEnd:
		fmt.Println("reasoning done:", p.Part.Text)
	case provider.SourceEvent:
		fmt.Println("source:", p.Source.URL)
	case provider.FinishPart:
		fmt.Println("finish reason:", p.Reason, "usage:", p.Usage.TotalTokens)
	}
}
```

## The iterator, Err(), and Close()

`TextStream.Parts()` (`iter.Seq[provider.StreamPart]`) is **single-use**:
calling it again after exhausting or abandoning it yields nothing. It spans
every step of a multi-step tool loop — between steps it executes any
requested tools and starts the next model stream automatically, so a single
`for range stream.Parts()` covers the whole run.

`stream.Err()` is `nil` until iteration ends; after an abnormal end it
holds the terminal error: a `*ai.RetryError` if a later step's stream
couldn't start, an `*ai.NoSuchToolError` if an unknown tool was requested,
or the underlying provider stream's mid-stream error. Check it after the
loop:

```go
for part := range stream.Parts() {
	// ...
}
if err := stream.Err(); err != nil {
	var retryErr *ai.RetryError
	if errors.As(err, &retryErr) {
		log.Fatalf("gave up after %d attempts: %v", retryErr.Attempts, retryErr.LastErr)
	}
	log.Fatal(err)
}
```

`stream.Close()` releases the underlying provider stream. It's idempotent
and safe to call at any point: before `Parts()` has ever been ranged over
(so the HTTP body doesn't leak if the caller decides not to consume the
stream), after `Parts()` has fully iterated or been abandoned (`Parts()`
already closes the stream itself in both cases, making `Close()` a no-op),
or mid-iteration. `defer stream.Close()` right after `StreamText` returns is
the standard pattern. `Close` is not safe for concurrent use with `Parts()`.

## Accessors

Valid once `Parts()` has been iterated (fully or partially):

- **`Text()`** — accumulated text of the final step.
- **`ReasoningText()`** — accumulated reasoning text of the final step.
- **`Sources()`** — `[]provider.SourcePart` accumulated via `SourceEvent`
  during the final step.
- **`Steps()`** — every step executed so far. If iteration stopped because
  of a `*ai.NoSuchToolError`, that step is still appended with `ToolCalls`
  populated but `ToolResults` `nil` (execution never ran) — check `Err()`
  rather than assuming every step completed.
- **`Usage()`** — summed `provider.Usage` across all steps.
- **`FinishReason()`** — the last step's finish reason.
- **`Messages()`** — the full conversation so far, same semantics as
  `GenerateTextResult.Messages` (see
  [Conversation continuation](generating-text.md#conversation-continuation)).
  Before any iteration, it's just the initial request messages.

## Suspension in streams

`StreamText` suspends the same way `GenerateText` does when a tool call
needs approval and none is available — see
[Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
for the full mechanics. Two entry points into that behavior:

- **Suspending mid-stream:** if a step's tool-call batch has an
  undecided approval-needing call, `TextStream` records that step (with no
  `ToolResults`), fires `OnStepFinish`, and ends iteration — the same as a
  natural end, not an error. `stream.Err()` is `nil`.
- **Suspending immediately on resume:** if `Messages`'s trailing unanswered
  tool-call batch (see [Generating text § Resume-from-Messages is its own capability](generating-text.md#resume-from-messages-is-its-own-capability))
  is itself still pending, `StreamText` returns a `*TextStream` that has
  never started a provider stream at all — `Parts()` yields zero parts,
  and iterating it (even zero times) is enough to make the accessors
  below valid.

In both cases:

- **`stream.PendingApprovals() []ai.ApprovalRequest`** reports the same
  value `GenerateTextResult.PendingApprovals` would for the equivalent
  `GenerateText` call — non-empty exactly when suspended.
- **`OnFinish`** still fires (with a `*GenerateTextResult` whose
  `PendingApprovals` is populated) — suspension is not an error, so it
  goes through the same finish path as natural completion, not `OnError`/
  `OnAbort`.
- **`Steps()`** is empty for the immediate-resume-suspension case (no model
  call ever happened), or ends with the tool-result-less suspended step for
  the mid-stream case.
- **`FinishReason()`** is `provider.FinishToolCalls` in both cases.
- **`Messages()`** ends with the assistant message carrying the suspended
  batch — round-trippable the same way as `GenerateTextResult.Messages`.

```go
stream, err := ai.StreamText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Delete the record for user 42.",
	Tools:  []ai.Tool{guardedDelete},
	MaxSteps: 2,
})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

for range stream.Parts() {
	// consume as usual; a suspending run ends iteration here with Err() == nil
}
if pending := stream.PendingApprovals(); len(pending) > 0 {
	fmt.Println("awaiting approval for:", pending[0].Call.Name)
}
```

## Timeout: the Chunk dimension

`GenerateTextOpts.Timeout.Chunk` bounds the max gap between yielded
`provider.StreamPart`s within a step — a stall watchdog specific to
`StreamText` (`Total` and `Step`, the other two `Timeout` dimensions, apply
to both `GenerateText` and `StreamText`):

```go
stream, err := ai.StreamText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Count to a million.",
	Timeout: &ai.Timeout{Chunk: 10 * time.Second},
})
```

If more than `Chunk` elapses between two yielded parts (the timer resets
on every part, including the step's first), iteration ends with a
`*ai.TimeoutError{Dimension: "chunk"}` from `stream.Err()` — distinct from
the caller's own `ctx` being canceled, which still surfaces via `OnAbort`
as before. See [Generating text § Timeout](generating-text.md#timeout-total-step-and-chunk)
for the full Total/Step/Chunk reference and the `TimeoutError`-vs-`OnAbort`
distinction.

## SmoothStream

`ai.SmoothStream(parts iter.Seq[provider.StreamPart], opts ai.SmoothOpts) iter.Seq[provider.StreamPart]`
re-chunks `TextDelta`s into smaller, more evenly sized deltas for driving a
UI at a steady cadence — it doesn't change the total text, only how it's
broken into deltas over time. Apply it downstream of `stream.Parts()`:

```go
smoothed := ai.SmoothStream(stream.Parts(), ai.SmoothOpts{
	Chunking: ai.ChunkingWord, // or ai.ChunkingLine
	Delay:    50 * time.Millisecond,
})
for part := range smoothed {
	if td, ok := part.(provider.TextDelta); ok {
		fmt.Print(td.Text)
	}
}
```

- **`Chunking`** — `ai.ChunkingWord` (default when empty) splits on
  whitespace boundaries; `ai.ChunkingLine` splits on newlines. Any
  unrecognized value falls back to word chunking. Each emitted chunk is the
  content unit *plus* its trailing delimiter — word mode emits `"hello "`
  (not `"hello"` then `" "` separately), line mode emits `"first line\n"`.
  A word/line split across multiple input `TextDelta`s is buffered and
  coalesced: nothing is emitted for a partial word/line until its trailing
  delimiter (or the stream's end) arrives.
- **`Delay`** — slept after every part `SmoothStream` emits (both
  re-chunked text deltas and passed-through parts). **Divergence from
  Vercel AI SDK's `smoothStream`:** that function defaults to a 10ms delay;
  `SmoothStream` applies **no implicit default** — zero means no delay at
  all, and callers who want one must set `Delay` explicitly. This keeps
  behavior predictable and keeps tests (which use `Delay: 0`) fast and
  deterministic.

Only `TextDelta` parts are re-chunked. Every other `StreamPart` — including
`ReasoningDelta`, passed through completely untouched even though it's also
free-form text — flushes any currently-buffered text first (as a
`TextDelta`), then is yielded unchanged. Any text still buffered when the
inner sequence ends is flushed as a final `TextDelta`.

### OnChunk ordering vs SmoothStream

`GenerateTextOpts.OnChunk`, if set, always sees the provider's original,
**unsmoothed** parts — it fires from inside `TextStream.Parts()` itself,
before any `SmoothStream` wrapping a caller might apply downstream. Code
that needs to observe both the raw cadence (e.g. for telemetry) and a
smoothed cadence (e.g. for UI rendering) can do both: `OnChunk` for the
former, `SmoothStream(stream.Parts(), ...)` for the latter.

## Source of truth

- [`provider/stream.go`](../../provider/stream.go)
- [`ai/stream_text.go`](../../ai/stream_text.go)
- [`ai/timeout.go`](../../ai/timeout.go) (`Timeout`'s chunk-stall watchdog)
- [`ai/smooth.go`](../../ai/smooth.go)
- [`ai/approval.go`](../../ai/approval.go), [`ai/runtime_context.go`](../../ai/runtime_context.go)

See also: [Generating text](generating-text.md) for `StreamText`'s options
and the tool loop; [Reasoning](reasoning.md) for how `ReasoningDelta`/
`ReasoningEnd` map to signed/redacted content; [Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
for the full approval mechanics behind [Suspension in streams](#suspension-in-streams).
