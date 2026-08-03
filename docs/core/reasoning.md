# Reasoning

Several providers expose a model's "thinking"/reasoning process alongside
its answer. `go-ai-sdk` represents this uniformly as
`provider.ReasoningPart` (in `Response.Content`) and
`provider.ReasoningDelta`/`ReasoningEnd` (in streams), regardless of which
provider-specific mechanism produced it.

```go
type ReasoningPart struct {
	Text      string
	Redacted  bool
	Signature string
}
```

`Signature` and `Redacted` are Anthropic-specific: `Signature` preserves
the cryptographic signature Anthropic attaches to a visible thinking block,
required to round-trip the block back to the API on a later turn; a
redacted block sets `Redacted` true and puts the opaque encrypted payload
in `Text` rather than readable reasoning.

## Requesting reasoning: GenerateTextOpts.Reasoning

`GenerateTextOpts.Reasoning` (`*provider.ReasoningConfig`) is a single,
provider-agnostic way to request more (or less) reasoning effort, mapped by
each provider to its own native knob:

```go
type ReasoningConfig struct {
	Effort       string // "", "minimal", "low", "medium", "high" — passed through, not validated
	BudgetTokens *int   // explicit thinking-token budget
}
```

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  anthropic.New().Model("claude-sonnet-5"),
	Prompt: "What is 17 * 24? Think it through.",
	Reasoning: &provider.ReasoningConfig{
		Effort: "high",
	},
})
```

`Effort` and `BudgetTokens` may be set together — a provider that only
understands one of the two uses that one; where both map to the same knob
(anthropic, geminicompat, bedrock), an explicit `BudgetTokens` wins over
`Effort`. Set `BudgetTokens` directly when you want an exact token count
instead of one of the four named efforts:

```go
Reasoning: &provider.ReasoningConfig{
	BudgetTokens: ptr(2000),
},
```

### Per-provider mapping

| Provider | Wire mechanism | Notes |
|---|---|---|
| openaicompat-based (OpenAI, Azure, Groq, xAI, DeepSeek, Cerebras, Together, Fireworks, Perplexity) | `reasoning_effort` | Sends `Effort` verbatim; `BudgetTokens` has no wire equivalent here and is ignored. |
| Anthropic | `thinking: {"type": "enabled", "budget_tokens": N}` | `N` is `BudgetTokens` if set, else resolved from `Effort` via `EffortBudgetTokens` (below). Omits `thinking` entirely if neither resolves. |
| Google / Vertex AI (geminicompat) | `generationConfig.thinkingConfig: {"thinkingBudget": N, "includeThoughts": true}` | Same budget resolution as Anthropic; `includeThoughts` is always `true` whenever a budget resolves (not independently configurable via `ReasoningConfig`). |
| Amazon Bedrock | `additionalModelRequestFields.thinking: {"type": "enabled", "budget_tokens": N}` | Same budget resolution and wire shape as Anthropic, nested under Bedrock's Converse-specific `additionalModelRequestFields`. Merged per sub-key with any `additionalModelRequestFields` set via `ProviderOptions` — see the precedence note below. |
| Cohere, Mistral | — (no-op) | Neither has a reasoning/thinking knob; `Reasoning` is silently ignored, no wire field is sent. |

### EffortBudgetTokens: the effort → token-budget table

For the three token-budget providers (Anthropic, geminicompat, Bedrock),
`Effort` is resolved to an explicit budget via the exported
`provider.EffortBudgetTokens` table:

| `Effort` | Budget tokens |
|---|---|
| `"minimal"` | 1024 |
| `"low"` | 4096 |
| `"medium"` | 8192 |
| `"high"` | 16384 |
| `""` or unrecognized | not resolved (`ok == false`) — no budget sent |

```go
budget, ok := provider.EffortBudgetTokens("medium") // 8192, true
```

Anthropic and Bedrock do **not** validate `max_tokens > budget_tokens` or
Anthropic's temperature restriction under extended thinking — an
out-of-range value passes through and surfaces as a provider
`*ai.APICallError`, not a client-side validation error.

### ProviderOptions precedence

`ProviderOptions` still merges last and wins on a wire-key collision — the
repo-wide convention (see [Provider options](provider-options.md)) applies
to `Reasoning`'s output exactly like every other `Call` field. Setting
`ProviderOptions["anthropic"]["thinking"]` (or the equivalent
`additionalModelRequestFields` on bedrock) overrides whatever `Reasoning`
would otherwise have produced. On Bedrock specifically, the merge is scoped
per sub-key within `additionalModelRequestFields`: a `Reasoning`-derived
`"thinking"` entry and an unrelated `ProviderOptions`-set entry under a
different sub-key coexist; only an exact key collision (both setting
`"thinking"`) lets `ProviderOptions` win.

## Enabling it per provider (manual ProviderOptions)

The examples below predate `GenerateTextOpts.Reasoning` and still work
unchanged as a manual escape hatch — useful for a provider-specific field
`ReasoningConfig` doesn't expose (e.g. Anthropic's `type` values other than
`"enabled"`).

### Anthropic: extended thinking

Anthropic's extended thinking is enabled per call via `ProviderOptions`,
not a typed option — set `ProviderOptions["anthropic"]["thinking"]` to
`map[string]any{"type": "enabled", "budget_tokens": N}`:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  anthropic.New().Model("claude-sonnet-5"),
	Prompt: "What is 17 * 24? Think it through.",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{
			"thinking": map[string]any{
				"type":          "enabled",
				"budget_tokens": 2000,
			},
		},
	},
})
```

When enabled, `thinking`/`redacted_thinking` blocks in the response surface
as `provider.ReasoningPart`s: `redacted_thinking` sets `Redacted` and puts
the opaque payload in `Text`; a regular `thinking` block sets `Signature`.
Streaming surfaces the thinking text as `provider.ReasoningDelta` and the
finished block (including any signature) as `provider.ReasoningEnd`.

### DeepSeek: `reasoning_content`

DeepSeek-R1-style models (routed through the shared `openaicompat` base)
report reasoning via a `reasoning_content` field alongside the normal
`content` field — no signature, no redaction, just plain text. Non-streamed
responses surface it as a single `ReasoningPart{Text: ...}`; streamed
responses surface it as a run of `ReasoningDelta`s with no closing
`ReasoningEnd` (see the incremental-assembly note below).

### Bedrock: `reasoningContent`

Amazon Bedrock's Converse API reports reasoning via a `reasoningContent`
block that, like Anthropic, distinguishes a signed visible block from a
redacted one: a populated `reasoningText` (with its own `text`/`signature`
fields) carries the signed, replayable reasoning, while `redactedContent`
carries an opaque payload. The SDK maps this the same way as Anthropic's
thinking blocks — signed text sets `Signature`, redacted content sets
`Redacted`.

## Redacted handling

`Response.ReasoningText()` concatenates all **non-redacted**
`ReasoningPart`s in `Content`, skipping redacted ones — their `Text` holds
opaque provider-encrypted data, not readable reasoning, so including it
would leak ciphertext into a user-facing text accessor. Redacted parts
remain present in `Content` (and round-trip correctly back to the
provider on a later turn) — only the `ReasoningText()` aggregation filters
them out:

```go
result, _ := ai.GenerateText(ctx, opts)
fmt.Println(result.ReasoningText)                  // redacted parts excluded
fmt.Println(len(result.Steps[0].Response.Content))  // redacted parts still present
```

## Signature round-trip and the unsigned-part skip rule

When an assistant message containing `ReasoningPart`s is sent back to
Anthropic (or Bedrock) on a later turn, the SDK automatically reorders them
to lead the message content — both APIs require thinking/reasoning blocks
to lead an assistant turn. Within that reordering:

- A redacted part (`Redacted: true`) becomes a `redacted_thinking` /
  redacted `reasoningContent` block, replayed as-is.
- A part with a non-empty `Signature` becomes a signed `thinking` /
  `reasoningText` block, replayed with its signature.
- A non-redacted part with **no** signature cannot form a valid replayable
  block — both APIs require a signature on replayed thinking blocks — so it
  is **skipped** (the same convention applied to `SourcePart`, which is
  informational and not replayable). This can happen if a `ReasoningPart`
  was constructed by hand (e.g. via `ExtractReasoningMiddleware`, below)
  rather than received from the provider.

## Usage detail fields

`provider.Usage` carries two fields specifically for reasoning:

- **`ReasoningTokens`** — the portion of `OutputTokens` spent on
  reasoning/thinking content (from OpenAI-compatible
  `usage.completion_tokens_details.reasoning_tokens`). Zero when the
  provider doesn't report it.
- **`CachedInputTokens`** — not reasoning-specific, but commonly reported
  alongside it (Anthropic `cache_read_input_tokens`, OpenAI-compatible
  `usage.prompt_tokens_details.cached_tokens`).

## ExtractReasoningMiddleware

For models that signal "thinking" with an inline tag in the text output
rather than a dedicated reasoning content type (e.g. some DeepSeek-compatible
endpoints using `<think>...</think>`), `ai.ExtractReasoningMiddleware` pulls
`<tagName>...</tagName>` spans out of the text and re-emits them as
reasoning content:

```go
wrapped := ai.ExtractReasoningMiddleware(model, ai.ExtractReasoningOpts{
	TagName: "think",
})

resp, err := wrapped.Generate(ctx, provider.Call{
	Messages: []provider.Message{provider.UserText("What is 40+2?")},
})
// resp.Text() == "The answer is 42."
// resp.ReasoningText() == "carry the 1"
```

`ExtractReasoningOpts`:

- **`TagName`** (required) — the tag name without angle brackets, e.g.
  `"think"`.
- **`StartWithReasoning`** (`bool`) — when true, the model is assumed to
  omit the opening tag and begin its response already "inside" a reasoning
  span; content from the very start is treated as reasoning until the
  closing tag (or the end of the response, if it never arrives). When false
  (the default), an orphan closing tag with no matching opener is inert —
  it passes through as ordinary text verbatim.

```go
// StartWithReasoning=true: the raw text has no opening tag.
wrapped := ai.ExtractReasoningMiddleware(model, ai.ExtractReasoningOpts{
	TagName:            "think",
	StartWithReasoning: true,
})
```

**Incremental guarantees:** the stream path never buffers more than the
longest unresolved prefix of the tag it's currently watching for (the open
tag while outside reasoning, the close tag while inside), so text/reasoning
content flows to the caller as it arrives rather than being held back
pending a later determination. A tag marker split across stream deltas
(e.g. `"<th"` then `"ink>"`) is still recognized correctly, since the
unresolved prefix carries over between feeds.

## Source of truth

- [`provider/call.go`](../../provider/call.go) (`Call.Reasoning`,
  `ReasoningConfig`, `EffortBudgetTokens`)
- [`ai/options.go`](../../ai/options.go) (`GenerateTextOpts.Reasoning`)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`reasoning_effort`)
- [`providers/anthropic/wire.go`](../../providers/anthropic/wire.go)
  (`wireThinking`, `resolveBudgetTokens`)
- [`internal/geminicompat/wire.go`](../../internal/geminicompat/wire.go)
  (`wireThinkingConfig`)
- [`providers/bedrock/wire.go`](../../providers/bedrock/wire.go)
  (`additionalModelRequestFields.thinking`, `applyProviderOptions` per-sub-key
  merge)
- [`provider/message.go`](../../provider/message.go) (`ReasoningPart`,
  `SourcePart`)
- [`provider/response.go`](../../provider/response.go) (`ReasoningText()`)
- [`providers/anthropic/anthropic.go`](../../providers/anthropic/anthropic.go)
  (package doc comment), [`providers/anthropic/wire.go`](../../providers/anthropic/wire.go)
  (`assistantBlocks`)
- [`providers/bedrock/wire.go`](../../providers/bedrock/wire.go)
  (`reasoningPartFromWire`, assistant-block assembly)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`ReasoningContent`)
- [`ai/middleware.go`](../../ai/middleware.go)
  (`ExtractReasoningMiddleware`)

See also: [Streaming](streaming.md) for `ReasoningDelta`/`ReasoningEnd` in
the stream-part reference; [Middleware and registry](middleware-and-registry.md)
for the other built-in middlewares and composition order.
