# Anthropic

`providers/anthropic` talks to Anthropic's Messages API (`/v1/messages`)
directly. Anthropic has no embeddings API, so this package intentionally
does not implement `provider.EmbeddingModel`.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/anthropic"

p := anthropic.New(
	anthropic.WithAPIKey("sk-ant-..."),              // defaults to os.Getenv("ANTHROPIC_API_KEY")
	anthropic.WithBaseURL("https://api.anthropic.com"), // the default
	anthropic.WithHTTPClient(http.DefaultClient),
)

model := p.Model("claude-sonnet-5")
```

Every request is sent with two headers, not an `Authorization: Bearer`
header: `x-api-key: <key>` and `anthropic-version: 2023-06-01` (a constant
baked into the package, not configurable via an `Option`).

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)` → `provider.LanguageModel`.
- **Tool calling** — same `Model(id)`; forced single-tool-call is also how
  structured output is implemented (see below).
- **Structured output** — no native JSON mode:
  `languageModel.Capabilities().NativeJSON` is `false`, so `ai.GenerateObject`
  falls back to its tool-mode path — it injects a single `ToolDef` built from
  the target schema and sets `ToolChoice{Mode: provider.ToolChoiceTool}` to
  force that call, then decodes the tool-call arguments as the object.
- **Extended thinking** — see below.
- **No embeddings, no images, no speech, no transcription** — not exposed by
  this package.

## Quirks and notes

- **`max_tokens` defaults to 4096.** `Call.MaxTokens` is a `*int`; when the
  caller leaves it `nil`, `buildMessagesRequest` substitutes `defaultMaxTokens
  = 4096` rather than omitting the field — the Messages API requires
  `max_tokens` on every request, so there's no "let the API default it"
  option the way there is for temperature or top_p.
  (`providers/anthropic/anthropic.go:34`, `providers/anthropic/wire.go:210-213`.)
- **Structured output is tool-mode, not native JSON.** `Capabilities()`
  returns `provider.Capabilities{NativeJSON: false}`
  (`providers/anthropic/language_model.go:26-28`); `ai.GenerateObject`'s
  `buildObjectCall` checks that flag and, when false, builds a forced
  tool-choice call instead of setting `ResponseFormat` (`ai/generate_object.go`,
  `buildObjectCall`).
- **Thinking blocks must lead the assistant message on replay.** When an
  assistant turn containing `provider.ReasoningPart`s is sent back on a later
  turn, `assistantBlocks` partitions reasoning parts out and prepends them
  ahead of every other block type — text, tool calls — because the Messages
  API requires `thinking`/`redacted_thinking` blocks to come first in an
  assistant turn. A non-redacted reasoning part with no `Signature` can't
  form a valid replayable block and is silently skipped.
  (`providers/anthropic/wire.go:344-385`, doc comment on `assistantBlocks`.)
- **PDF is the only supported `FilePart` media type.** A `FilePart` with any
  `MediaType` other than `application/pdf` (matched case-insensitively, MIME
  parameters ignored) returns an error rather than being dropped; `Filename`,
  if set, becomes the `document` block's `title`.
  (`providers/anthropic/wire.go:289-342`, `isPDFMediaType`, `userBlocks`.)
- **`ToolChoiceNone` omits `tools` entirely**, not just `tool_choice` — Chat
  Completions-style providers merely drop the tool_choice field, but this
  package skips sending `Tools`/`ToolChoice` altogether when the caller asks
  for no tools. (`providers/anthropic/wire.go:225-235`.)
- **`cache_creation_input_tokens`** (prompt-cache write count, distinct from
  `Usage.CachedInputTokens`, which is cache *reads*) surfaces under
  `Response.ProviderMetadata["anthropic"]["cache_creation_input_tokens"]`
  when non-zero, both for `Generate` (`convertResponse`,
  `providers/anthropic/wire.go:478-484`) and for streaming (`cacheCreationMetadata`,
  `providers/anthropic/language_model.go:341-354`).

## Extended thinking

Enabled per call via `ProviderOptions`, not a typed field — set
`ProviderOptions["anthropic"]["thinking"]` to `map[string]any{"type":
"enabled", "budget_tokens": N}` (same example as
[Reasoning](../core/reasoning.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("claude-sonnet-5"),
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

`thinking` blocks surface as `provider.ReasoningPart{Signature: ...}`;
`redacted_thinking` blocks set `Redacted: true` with the opaque payload in
`Text`. See [Reasoning](../core/reasoning.md) for the full signature
round-trip contract.

## ProviderOptions

Entries under `ProviderOptions["anthropic"]` are shallow-merged into the raw
`/v1/messages` request body verbatim — raw wire key names, no translation.
Verified directly against `providers/anthropic/provideroptions_test.go`: an
option key can override an SDK-built field (`temperature`), and a novel key
not otherwise exposed by the SDK (`top_k`) passes straight through:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("claude-sonnet-5"),
	Prompt: "Explain the Go scheduler in one sentence.",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{
			"temperature": 0.9, // overrides Call.Temperature
			"top_k":       5,   // passthrough key, not typed on Call
		},
	},
})
```

## Source of truth

- [`providers/anthropic/anthropic.go`](../../providers/anthropic/anthropic.go)
  (package doc comment, `Option`s, `defaultMaxTokens`)
- [`providers/anthropic/language_model.go`](../../providers/anthropic/language_model.go)
  (`Capabilities`, `doRequest` headers, streaming)
- [`providers/anthropic/wire.go`](../../providers/anthropic/wire.go)
  (`buildMessagesRequest`, `assistantBlocks`, `isPDFMediaType`,
  `applyProviderOptions`)
- [`providers/anthropic/provideroptions_test.go`](../../providers/anthropic/provideroptions_test.go)
- [`ai/generate_object.go`](../../ai/generate_object.go) (`buildObjectCall`,
  tool-mode fallback)

See also: [Reasoning](../core/reasoning.md) for the extended-thinking worked
example and signature round-trip rules; [Provider options](../core/provider-options.md)
for the general `ProviderOptions` merge contract.
