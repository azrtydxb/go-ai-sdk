# Provider options

`ProviderOptions` is the SDK's escape hatch for provider-specific request
parameters that don't have a dedicated field on `provider.Call` (or
`ai.GenerateTextOpts` etc.); `ProviderMetadata` is its response-side
counterpart. Both use the same namespacing convention.

## The namespaced raw-wire-key convention

```go
type Call struct {
	// ...
	ProviderOptions map[string]any
}
```

`ProviderOptions` is keyed by provider name (the value returned by the
model's `ProviderName()` — for the OpenAI/Gemini-compatible bases, this is
the preset's `Config.Name`, e.g. `"openai"`, `"groq"`, `"azure"`). Each
provider looks up **only its own key**; entries under other providers' keys
are ignored, so it's safe to build one options map covering several
providers and reuse it across models:

```go
opts := map[string]any{
	"anthropic": map[string]any{"top_k": 5},
	"openai":    map[string]any{"seed": 42},
}
```

The value under a matching key must itself be a `map[string]any` (other
value types are ignored); its entries are shallow-merged into the top-level
JSON object the SDK builds for the request, **after** the SDK builds it —
so option entries win over SDK-set fields. `{"anthropic": {"temperature":
0.9}}` overrides `Call.Temperature`. Keys not otherwise exposed as a typed
field (e.g. `{"anthropic": {"top_k": 5}}`) pass through untouched.

Critically, the value on each key is the **raw wire key name** for that
provider's API — not a Go-idiomatic or camelCased name the SDK translates
for you. The anthropic thinking example from [Reasoning](reasoning.md) uses
`"budget_tokens"`, exactly as Anthropic's Messages API spells it, because
there is no translation layer between what you write and what goes over
the wire.

**Divergence from the Vercel AI SDK:** Vercel's TypeScript SDK is documented
as following the same per-provider-namespace shape (`providerOptions: {
anthropic: {...} }`), with its provider packages defining typed, camelCased
option keys that the SDK maps onto the wire format for you — e.g.
`budgetTokens` in TypeScript is documented as becoming `budget_tokens` on
the wire. (Per the sourcing note in
[Migrating from the Vercel AI SDK](../migrating-from-vercel-ai-sdk.md):
this claim is based on Vercel's public documentation, not its source, since
this repository has no access to `ai`'s TypeScript codebase to verify
against.) `go-ai-sdk` has no such
per-provider options schema or translation step: what you put under
`ProviderOptions["anthropic"]` is merged into the request body verbatim, so
it must already be the wire key. Porting a Vercel AI SDK call means
snake_casing (or otherwise wire-matching) every provider option key by
hand, not just moving the map over.

```go
// Vercel AI SDK (TypeScript) — documented behavior, translated by the anthropic provider:
// providerOptions: { anthropic: { thinking: { type: 'enabled', budgetTokens: 2000 } } }

// go-ai-sdk — raw wire key, no translation:
ProviderOptions: map[string]any{
	"anthropic": map[string]any{
		"thinking": map[string]any{"type": "enabled", "budget_tokens": 2000},
	},
},
```

## Per-provider merge point

Where the merge happens depends on the request shape:

- **Top-level JSON object requests** (chat completions, messages, images,
  speech) — the SDK marshals its own request struct to JSON, then
  shallow-merges `ProviderOptions[name]`'s entries into that decoded JSON
  object, then re-marshals. No merge (and no unmarshal/marshal round trip)
  happens when there's nothing to merge, which is the common case.
- **Multipart form requests** (`openaicompat` and ElevenLabs
  transcription) — there's no single JSON object to merge into, so each
  entry in `ProviderOptions[name]` is instead written as an extra
  multipart form field, with its value stringified via `fmt.Sprint`.

Setting `ProviderOptions` is a no-op for any key that doesn't match the
provider actually being called — passing a map keyed for every provider you
might use, unconditionally, is the intended usage pattern (see the
composed example above).

### Reasoning is no exception

`GenerateTextOpts.Reasoning`/`Call.Reasoning` (see
[Reasoning](reasoning.md#requesting-reasoning-generatetextoptsreasoning))
follows the same rule as every other typed `Call` field: `ProviderOptions`
merges in **after** the SDK builds the request from `Reasoning`, so a
`ProviderOptions["anthropic"]["thinking"]` entry (or the equivalent
`additionalModelRequestFields` shape on Bedrock) always wins over whatever
`Reasoning` would otherwise have produced for that same wire key. This
holds even though the roadmap that introduced `Reasoning` initially
described the opposite precedence — the repo-wide "`ProviderOptions` merges
last and wins" convention documented on this page takes priority, and is
what actually shipped.

## ProviderMetadata

```go
type Response struct {
	// ...
	ProviderMetadata map[string]any
}

type FinishPart struct {
	// ...
	ProviderMetadata map[string]any
}
```

`ProviderMetadata` is the response-side mirror of `ProviderOptions`: data
that has no home in `Response`'s typed fields, namespaced the same way
(`ProviderMetadata["anthropic"]`, `ProviderMetadata["openai"]`). Each
provider decides what, if anything, to populate under its own key; it's
`nil` when the provider has nothing to report. `provider.FinishPart`
carries the same shape for the streaming API — see
[Streaming](streaming.md#streampart-reference).

### Per-provider contents

- **Anthropic** — `ProviderMetadata["anthropic"]["cache_creation_input_tokens"]`,
  populated when the response reports a non-zero
  `cache_creation_input_tokens` in its usage block (the number of input
  tokens *written* to the prompt cache, distinct from
  `Usage.CachedInputTokens`, which tracks tokens *read* from cache).
- **OpenAI-compatible (`openaicompat`)** — `ProviderMetadata["<name>"]["system_fingerprint"]`,
  populated from the first non-empty `system_fingerprint` observed
  (identifies the backend configuration that served the request; `<name>`
  is the preset's `Config.Name`, e.g. `"openai"`, `"groq"`).

```go
result, _ := ai.GenerateText(ctx, opts)
meta := result.Steps[0].Response.ProviderMetadata
fmt.Println(meta["anthropic"]) // map[cache_creation_input_tokens:128]
```

## Source of truth

- [`provider/call.go`](../../provider/call.go) (`Call.ProviderOptions` doc
  comment)
- [`provider/response.go`](../../provider/response.go)
  (`Response.ProviderMetadata`)
- [`provider/stream.go`](../../provider/stream.go)
  (`FinishPart.ProviderMetadata`)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`, `applyProviderOptionsForm`, `SystemFingerprint`)
- [`providers/anthropic/wire.go`](../../providers/anthropic/wire.go)
  (`applyProviderOptions`, `cache_creation_input_tokens`)

See also: [Reasoning](reasoning.md) for the Anthropic thinking worked
example; [Middleware and registry](middleware-and-registry.md) for
`DefaultSettingsMiddleware`'s `ProviderOptions` merge semantics.
