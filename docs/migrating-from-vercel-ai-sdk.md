# Migrating from the Vercel AI SDK

`go-ai-sdk` is a deliberate Go port of the [Vercel AI SDK](https://sdk.vercel.ai):
same core concepts (`generateText`/`streamText`, `generateObject`/
`streamObject`, tools, middleware, a provider registry), same naming
wherever Go idiom allows it, expressed with Go's type system and
concurrency primitives instead of TypeScript's. This page is a working
reference for porting TypeScript call sites, not a tutorial — see
[Getting started](getting-started.md) for that.

**A note on sourcing:** claims about the Vercel AI SDK's TypeScript
behavior below are based on its public documentation, not its source —
this repository has no access to `ai`'s TypeScript codebase to verify
against. Treat "Vercel's SDK is documented as..." phrasing as exactly that:
what the docs say, current as of when this page was written. If your
installed version behaves differently, trust your own testing over this
page.

## API mapping

Every core entry point, side by side. `ai.X` here is always the Go
package `github.com/azrtydxb/go-ai-sdk/ai`, imported as `ai`.

| TypeScript (`ai`) | Go (`go-ai-sdk`) | Notes |
|---|---|---|
| `generateText(opts)` | `ai.GenerateText(ctx, ai.GenerateTextOpts{...})` | `ctx` is an explicit first argument, not implicit; returns `(*ai.GenerateTextResult, error)` instead of throwing. |
| `streamText(opts)` | `ai.StreamText(ctx, ai.GenerateTextOpts{...})` | Returns `(*ai.TextStream, error)` synchronously — no `await`; the stream itself is consumed via `for part := range stream.Parts()` (`iter.Seq`), not an async iterator or React hook. |
| `generateObject(opts)` | `ai.GenerateObject[T](ctx, ai.GenerateObjectOpts{...})` | `T` (a Go generic type parameter) replaces the Zod `schema` — see [Zod → struct tags](#zod--struct-tags-and-json-schema) below. |
| `streamObject(opts)` | `ai.StreamObject[T](ctx, ai.GenerateObjectOpts{...})` | Returns `*ai.ObjectStream[T]`; `Partials()` (`iter.Seq[T]`) replaces `partialObjectStream`, `Final()` replaces awaiting `object`. |
| `embed(opts)` | `ai.Embed(ctx, ai.EmbedOpts{...})` | Single value → single vector. |
| `embedMany(opts)` | `ai.EmbedMany(ctx, ai.EmbedManyOpts{...})` | Batches internally per `EmbeddingModel.MaxBatchSize()`. |
| `generateImage(opts)` | `ai.GenerateImage(ctx, ai.GenerateImageOpts{...})` | |
| `generateSpeech(opts)` | `ai.GenerateSpeech(ctx, ai.GenerateSpeechOpts{...})` | |
| `transcribe(opts)` | `ai.Transcribe(ctx, ai.TranscribeOpts{...})` | |
| `tool({ description, inputSchema, execute })` | `ai.NewTool[Args](name, description, fn)` | `Args` is inferred by Go's generics from `fn`'s signature; schema is derived by reflection, not written by hand (see below). |
| `cosineSimilarity(a, b)` | `ai.CosineSimilarity(a, b []float64) (float64, error)` | Returns an error instead of `NaN`/throwing on mismatched lengths or a zero vector. |
| `createProviderRegistry({...})` | `ai.NewRegistry()` + `reg.Register(name, provider)` | `reg.LanguageModel("anthropic:claude-sonnet-5")` etc. replace `registry.languageModel(id)`. |
| `wrapLanguageModel({ model, middleware })` | `ai.WrapModel(model, wrap)`, or one of the built-in middlewares directly | See [Middleware names](#middleware-names) below. |
| `smoothStream({ chunking, delayInMs })` | `ai.SmoothStream(stream.Parts(), ai.SmoothOpts{Chunking, Delay})` | Applied to a `stream.Parts()` sequence, not passed as a `transform` option — see [Divergences](#documented-divergences). |
| `stopWhen: stepCountIs(n)` | `StopWhen: ai.StepCountIs(n)` in `GenerateTextOpts` | `stopWhen` accepts an array of conditions in TS; Go takes a single `func(steps []Step) bool` — compose multiple conditions by hand (`||`/`&&` inside the closure). |
| `prepareStep` | `PrepareStep func(stepIndex int, plan ai.StepPlan) (ai.StepPlan, bool)` | See [stopWhen / prepareStep semantics](#stopwhen--preparestep-semantics) for the model-swap persistence difference. |
| `onStepFinish` | `OnStepFinish func(step ai.Step)` | Same intent, both APIs. |
| `onChunk` | `OnChunk func(part provider.StreamPart)` | |
| `onFinish` | `OnFinish func(result *ai.GenerateTextResult)` | |
| `onError` | `OnError func(err error)` | See the [validation-error exclusion](#documented-divergences) divergence (#5). |
| `activeTools` | `ActiveTools []string` | See the [ActiveTools resolution](#documented-divergences) divergence (#6). |
| `experimental_repairToolCall` | `RepairToolCall func(ctx, ai.ToolCallRecord, error) (ai.ToolCallRecord, bool)` | One retry, same as Vercel's documented behavior; Go signature returns `(call, ok bool)` instead of `Promise<ToolCall | null>`. |
| `maxRetries` | `MaxRetries *int` (pointer; `nil` = default 2) | |
| `maxOutputTokens` | `MaxTokens *int` | |
| `temperature` / `topP` / `stopSequences` | `Temperature` / `TopP` / `StopSequences` (all pointers where TS allows omission) | |
| `providerOptions` | `ProviderOptions map[string]any` | **Values are raw wire keys, not translated option names** — the single biggest divergence; see below. |
| `experimental_telemetry` (OTel-based) | `ai.TelemetryMiddleware(model, telemetryImpl)` + `ai.Telemetry` interface | Not OpenTelemetry — see [Telemetry is an interface, not OTel](#telemetry-is-an-interface-not-otel). |
| MCP tools (`experimental_createMCPClient`) | `mcp.NewClient(transport)` + `mcp.Tools(ctx, client)` | Tools-only — see [MCP is tools-only](#mcp-is-tools-only). |
| `useChat` / `useCompletion` / `useObject` (React hooks) | *(not ported)* | UI-framework layer — see [Features NOT ported](#features-not-ported). |
| React Server Components streaming (`streamUI`, `createStreamableUI`) | *(not ported)* | Same reason. |

## Concept mapping

### Zod → struct tags and JSON Schema

Vercel's `generateObject`/`tool` take a Zod schema (`z.object({...})`)
that doubles as runtime validator and TypeScript type. Go has no
equivalent runtime schema library in the standard toolchain, so
`go-ai-sdk` derives a JSON Schema from a plain Go struct via reflection —
the struct *is* the schema, the same way it's also the decode target:

```go
type WeatherArgs struct {
	City string `json:"city"`
}

// TS: tool({ inputSchema: z.object({ city: z.string() }), execute: ... })
weatherTool := ai.NewTool("get_weather", "Get the current weather for a city",
	func(ctx context.Context, args WeatherArgs) (any, error) {
		return fmt.Sprintf("Sunny in %s", args.City), nil
	})
```

```go
type Recipe struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
}

// TS: generateObject({ schema: z.object({ name: z.string(), ingredients: z.array(z.string()) }) })
result, err := ai.GenerateObject[Recipe](ctx, ai.GenerateObjectOpts{
	Model:  model,
	Prompt: "Give me a pasta recipe.",
})
```

There's no `.describe()`, `.min()`/`.max()`, `.optional()`, or refinement
chain — the schema builder reflects `json` struct tags and Go types only
(required-ness is inferred from the absence of `omitempty` and non-pointer
types; there's no dedicated validation-constraint annotation). If a Vercel
schema leans on Zod validators beyond basic shape (min/max length, regex,
enum), that validation has no direct equivalent here and needs to move
into your own post-decode check.

### providerOptions camelCase → wire keys

Covered in full in [Divergences](#documented-divergences) below —
`ProviderOptions` values are the provider's raw JSON wire field names, not
translated camelCase option names.

### stopWhen / prepareStep semantics

`StopWhen` takes one `func(steps []ai.Step) bool` instead of TS's
`stopWhen: condition | condition[]`; combine multiple conditions with `||`
inside the closure. `PrepareStep`'s model swap (`StepPlan.Model`) is
**sticky**: setting it at step N applies to every step after N too, until
`PrepareStep` swaps again — a deliberate divergence from a strictly
per-step override, chosen because "route to a cheaper model from step 3
onward" is the common case and re-asserting the swap every step to make it
"stick" would be pure boilerplate. See `ai.GenerateTextOpts.PrepareStep`'s
doc comment for the exact contract.

### Middleware names

| Vercel AI SDK | go-ai-sdk |
|---|---|
| `extractReasoningMiddleware({ tagName })` | `ai.ExtractReasoningMiddleware(model, ai.ExtractReasoningOpts{TagName: "think"})` |
| `simulateStreamingMiddleware()` | `ai.SimulateStreamingMiddleware(model)` |
| `defaultSettingsMiddleware({ settings })` | `ai.DefaultSettingsMiddleware(model, defaults provider.Call)` |
| `wrapLanguageModel({ model, middleware: [a, b] })` | Compose by nesting: `ai.WrapModel(model, a)` then wrap again, or call `a(b(model))`-style directly since every middleware here is just `func(provider.LanguageModel) provider.LanguageModel` |

### Streams: async iterators → iter.Seq

Vercel's `streamText` result exposes `textStream`/`fullStream` as async
iterables (`for await (const part of result.fullStream)`) backed by Web
Streams. `go-ai-sdk` exposes `TextStream.Parts()` as a Go 1.23+
`iter.Seq[provider.StreamPart]`, consumed with a plain `for range`:

```go
// TS
for await (const part of result.fullStream) { ... }

// Go
for part := range stream.Parts() { ... }
```

The Go iterator is **single-use** and synchronous — no `Promise`/`await`,
no backpressure management to think about, and no separate `textStream`
vs `fullStream` split: `Parts()` carries every part type (text, tool
calls, reasoning, sources, finish), and callers `switch` on the
concrete type (see [Streaming](core/streaming.md#streampart-reference)).
`stream.Err()` (checked after the loop) replaces a rejected promise or a
`part.type === 'error'` sentinel in the stream.

## Documented divergences

Consolidated from the individual guides — each one is explained in full
at its source page.

1. **`ProviderOptions` values are raw wire keys, not translated option
   names.** Vercel's per-provider option types (e.g. `anthropic.thinking`)
   are camelCased and translated onto the wire by each provider package.
   `go-ai-sdk` has no such translation layer: `ProviderOptions["anthropic"]`
   is a `map[string]any` merged **verbatim** into the JSON request body,
   using the field names the provider's HTTP API actually expects
   (typically `snake_case`). Vercel's documented `anthropic.thinking:
   { budgetTokens: 2000 }` becomes
   `map[string]any{"anthropic": map[string]any{"thinking": map[string]any{"budget_tokens": 2000}}}`
   here — porting a call means translating every option key by hand, not
   just moving the map over. See [Provider options](core/provider-options.md).

2. **`SmoothStream` has no default delay.** Vercel's SDK is documented as
   defaulting `smoothStream`'s `delayInMs` to 10ms. `ai.SmoothStream`
   applies **no implicit default** — `SmoothOpts.Delay` zero means no
   delay at all; set it explicitly to get pacing. See
   [Streaming § SmoothStream](core/streaming.md#smoothstream).

3. **Telemetry is a plain interface, not OpenTelemetry.** Vercel's
   `experimental_telemetry` is documented as built directly on OpenTelemetry
   conventions (span names, semantic attributes). `go-ai-sdk` ships zero
   external dependencies, so `ai.Telemetry` is a minimal
   `OnSpanStart`/`OnSpanEnd` seam you bridge to OTel (or anything else)
   yourself. See [Telemetry](core/telemetry.md) and
   [Telemetry is an interface, not OTel](#telemetry-is-an-interface-not-otel)
   below.

4. **MCP support is tools-only.** Vercel's `experimental_createMCPClient`
   is documented as supporting tools today with broader MCP surface
   evolving. `go-ai-sdk`'s `mcp` package is explicitly scoped to
   `initialize`, `tools/list`, and `tools/call` — no resources, prompts,
   sampling, roots, or server-initiated requests. See
   [MCP is tools-only](#mcp-is-tools-only) below and [MCP](mcp.md).

5. **`OnError` excludes argument-validation errors.** Vercel's `onError`
   callback documentation doesn't carve out an exception for
   argument-validation failures. `go-ai-sdk` explicitly does not invoke
   `OnError` (nor `OnFinish`) for a `nil` `Model` or malformed
   `Prompt`/`Messages` — those are reported solely via the function's
   returned error, in both `GenerateText` and `StreamText`, because
   validation runs before any model call is attempted (there's no started
   call for `OnError` to describe). See
   [Generating text § OnFinish and OnError](core/generating-text.md#onfinish-and-onerror).

6. **`ActiveTools` resolution is stricter than "offered but not
   executable."** Vercel's `activeTools` is documented as narrowing which
   tools are sent to the model. `go-ai-sdk`'s `ActiveTools` does that
   *and* treats a tool named outside the active set as **unknown**
   (`*ai.NoSuchToolError`) if the model calls it anyway — even though the
   tool is present in `Tools` and would otherwise execute fine. A `nil`
   `ActiveTools` means every tool in `Tools` is active; a non-nil, even
   empty, slice replaces the active set entirely (`ActiveTools: []string{}`
   is not "no filtering" — it disables every tool). See
   [Tools § ActiveTools](core/tools.md#activetools).

### Telemetry is an interface, not OTel

```go
type Telemetry interface {
	OnSpanStart(info SpanInfo)
	OnSpanEnd(info SpanInfo)
}
```

Bridge it to OpenTelemetry yourself: start a span in `OnSpanStart`, end it
in `OnSpanEnd`. A full sketch (including the stream-lifecycle span-ending
rules) is in [Telemetry § OTel bridge sketch](core/telemetry.md#otel-bridge-sketch).

### MCP is tools-only

`mcp.Tools(ctx, client)` adapts an MCP server's tools into `[]ai.Tool` for
`GenerateTextOpts.Tools`. There is no equivalent of resources, prompts,
sampling, or roots, and neither transport (stdio, Streamable HTTP)
supports the server initiating requests back to the client. Full scope
and known transport deviations are in [MCP § Limitations](mcp.md#limitations-v1).

## Features NOT ported

### Permanent — out of scope for this SDK

- **UI framework hooks** (`useChat`, `useCompletion`, `useObject`, and
  the rest of `@ai-sdk/react` / `@ai-sdk/svelte` / etc.) — these are
  framework-bound (React/Svelte/Vue) client-side state managers with no Go
  equivalent target; `go-ai-sdk` is a backend/server SDK.
- **React Server Components streaming** (`streamUI`, `createStreamableUI`,
  and other `ai/rsc` primitives) — inherently tied to React's RSC
  runtime, which has no Go analogue.

### Future — plausible, not yet implemented

- **Anthropic citations** — Anthropic's citations feature (source-grounded
  text spans) isn't surfaced as a distinct part type yet; only Google's
  `groundingMetadata` is mapped to `provider.SourcePart` today (see
  `core/generating-text.md` and the README's Sources section).
- **Provider-executed tools** (e.g. Anthropic/OpenAI server-side tools
  like web search or code execution that run on the provider's
  infrastructure rather than round-tripping through the caller) — not
  modeled; every tool call in `go-ai-sdk` today executes client-side via
  `ai.Tool.Execute`.
- **Native OpenTelemetry integration** — `ai.Telemetry` is the seam for
  it, but no built-in OTel exporter/bridge ships with the SDK (see
  divergence 3 above).

## Source of truth

- [`ai/options.go`](../ai/options.go), [`ai/generate_text.go`](../ai/generate_text.go),
  [`ai/stream_text.go`](../ai/stream_text.go) — `GenerateTextOpts` and the
  tool loop
- [`docs/core/`](core/) — the per-topic guides each divergence above is
  drawn from
- [`docs/mcp.md`](mcp.md) — MCP client scope and transport deviations
- [Architecture](architecture.md) — how the layers underneath these APIs
  fit together
