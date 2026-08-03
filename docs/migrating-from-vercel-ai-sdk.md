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

## AI SDK 6 delta

`go-ai-sdk` has reached **full parity with the AI SDK 6 core** (ai-sdk.dev,
snapshot 2026-08-03) as of wave 14 — the closing wave of the
[v6 parity roadmap](superpowers/plans/2026-08-03-v6-parity-roadmap.md). The
final gap re-audit (wave 14) found the port essentially at parity already;
the four remaining core-surface gaps it identified (an OTel bridge, tool
strict mode/input examples, per-tool input-streaming hooks, and structured
timeouts) are all shipped as of this wave — see the closing
[v6 parity final audit](superpowers/specs/2026-08-03-v6-parity-final-audit.md)
for the full have-list, deliberately-out-of-scope surface (UI/RSC/WebRTC/
DevTools), and the confirmed v7-only items below. This table is the
complete feature-by-feature status:

| AI SDK 6 feature | Status |
|---|---|
| Call settings: `topK`, `presencePenalty`, `frequencyPenalty`, `seed`, `headers` | **Shipped** — `GenerateTextOpts`/`provider.Call` fields `TopK`/`PresencePenalty`/`FrequencyPenalty`/`Seed`/`Headers`; see [Generating text § Additional call settings](core/generating-text.md#additional-call-settings-topk-penalties-seed-headers). |
| Structured timeouts (`timeout` object: total, per-step, stream-stall) | **Shipped** — `GenerateTextOpts.Timeout{Total, Step, Chunk}`/`*ai.TimeoutError{Dimension, Limit}`. An SDK-imposed bound firing is distinguished from the caller's own `ctx` being canceled (via a sentinel `context.Cause` per dimension): the former surfaces as `*ai.TimeoutError` via the returned error/`OnError`, the latter as the plain ctx error/`OnAbort`, exactly as before this feature existed. See [Generating text § Timeout](core/generating-text.md#timeout-total-step-and-chunk) and [Streaming § Timeout: the Chunk dimension](core/streaming.md#timeout-the-chunk-dimension). |
| `hasToolCall` / `isLoopFinished` stop conditions | **Shipped** — `ai.HasToolCall(names...)`, `ai.LoopFinished()`; see [Generating text § The multi-step tool loop](core/generating-text.md#the-multi-step-tool-loop). |
| `onAbort` | **Shipped** — `GenerateTextOpts.OnAbort`; see [Generating text § OnAbort](core/generating-text.md#onabort). |
| Multi-modal tool results | **Shipped** — `ai.ToolResultContent`, native on anthropic/bedrock, text-projected elsewhere; see [Tools § Multi-modal tool results](core/tools.md#multi-modal-tool-results). |
| Tool `strict` mode | **Shipped** — `ai.WithToolStrict()` (a `NewTool` option) sets `Tool.Strict()`/`provider.ToolDef.Strict`; honored as `"strict":true` by openaicompat-based providers, ignored (no wire param, no error) by anthropic, geminicompat, bedrock, cohere, mistral. See [Tools § Strict mode and input examples](core/tools.md#strict-mode-and-input-examples). |
| Tool `inputExamples` | **Shipped** — `ai.WithToolInputExamples[Args](examples ...Args)` sets `Tool.InputExamples()`/`provider.ToolDef.InputExamples`; native support on anthropic (`input_examples`), folded into tool `Description` text for every other provider via `ai.AddToolInputExamplesMiddleware`. See [Tools § Strict mode and input examples](core/tools.md#strict-mode-and-input-examples) and [§ AddToolInputExamplesMiddleware](core/tools.md#addtoolinputexamplesmiddleware). |
| Per-tool input-streaming lifecycle hooks (`onInputStart`/`onInputDelta`/`onInputAvailable`) | **Shipped** — `ai.WithToolInputCallbacks(ai.ToolInputCallbacks{OnInputStart, OnInputDelta, OnInputAvailable})`. `StreamText` fires all three (Start on the first delta, Delta per delta, Available once assembled, before `Execute`); `GenerateText` fires only `OnInputAvailable` (no deltas exist); neither fires for the `Output` tool-mode fallback's synthetic call. See [Tools § Per-tool input streaming hooks](core/tools.md#per-tool-input-streaming-hooks). |
| `extractJsonMiddleware` | **Shipped** — `ai.ExtractJSONMiddleware`; see [Middleware and registry § ExtractJSONMiddleware](core/middleware-and-registry.md#extractjsonmiddleware). |
| Image-model middleware (`wrapImageModel` equivalent) | **Shipped as a naming hook** — `ai.WrapImageModel`; no built-in image middlewares ship yet, and Vercel's full image-middleware interface (which can also rewrite `params`/results) isn't modeled — see [Middleware and registry § WrapImageModel](core/middleware-and-registry.md#wrapimagemodel). |
| AssemblyAI, Gladia, Rev.ai transcription providers | **Shipped** — see [Provider overview](providers/README.md) and each provider's page. |
| `stopWhen` consultation on every step | **Shipped, and Vercel-consistent** — `StopWhen` is consulted after every completed step (not only ones that requested tool calls). See [Generating text § The multi-step tool loop](core/generating-text.md#the-multi-step-tool-loop). |
| Output modes on `generateText` (`text`/`object`/`array`/`choice`/`json`, `Experimental_Output`) | **Shipped for `GenerateText`** — `GenerateTextOpts.Output` (`ai.OutputObject[T]`/`ai.OutputArray[T]`/`ai.OutputChoice`/`ai.OutputJSON`), extracted via `ai.OutputAs[T]`; see [Generating text § Output modes](core/generating-text.md#output-modes). **Not shipped for `StreamText`**: Vercel's `Experimental_Output` also streams partial output incrementally as a stream mode; `go-ai-sdk`'s `StreamText` returns the typed `ai.ErrOutputWithStreamText` immediately if `Output` is set — this remains the one known future item, tracked under [Future — plausible, not yet implemented](#future--plausible-not-yet-implemented) below, not a v6-core gap. |
| Reranking (`rerank`, `RerankingModel`, Cohere/Voyage/Mixedbread) | **Shipped** — `ai.Rerank`, `provider.RerankingModel`, `Registry.RerankingModel`; Cohere, Voyage, and Mixedbread all implement `provider.RerankingModel`. See [Embeddings § Reranking](core/embeddings.md#reranking), [Voyage](providers/voyage.md#reranking), and [Mixedbread](providers/mixedbread.md#reranking). |
| Unified `reasoning` option (effort/budget, per-provider mapping) | **Shipped** — `GenerateTextOpts.Reasoning`/`provider.ReasoningConfig{Effort, BudgetTokens}`, mapped to `reasoning_effort` (openaicompat), a resolved token budget via `provider.EffortBudgetTokens` (Anthropic, Google/Vertex AI, Bedrock), or ignored (Cohere, Mistral); see [Reasoning § Requesting reasoning](core/reasoning.md#requesting-reasoning-generatetextoptsreasoning). `ProviderOptions` still merges last and wins over `Reasoning` on a wire-key collision — the repo-wide `ProviderOptions` precedence convention was not special-cased for this option; see [Provider options § Reasoning is no exception](core/provider-options.md#reasoning-is-no-exception). A top-level per-message reasoning **enum** (v7-only — see below) is not modeled. |
| Full lifecycle-callback event set (call-start/end, tool-execution start/end, embed/rerank events) | **Shipped** — `GenerateTextOpts.OnModelCallStart`/`OnModelCallEnd`, `OnToolExecutionStart`/`OnToolExecutionEnd` (see [Generating text § Lifecycle callbacks](core/generating-text.md#lifecycle-callbacks-model-call-and-tool-execution)); `EmbedOpts`/`EmbedManyOpts.OnEmbedStart`/`OnEmbedEnd` (see [Embeddings § Embed](core/embeddings.md#embed)); `RerankOpts.OnRerankStart`/`OnRerankEnd` (see [Embeddings § Reranking](core/embeddings.md#reranking)). Every End-callback's error is the SAME error the caller's function returns (retry exhaustion already translated to `*ai.RetryError`). |
| RuntimeContext / application context passed into tool execution | **Shipped** — `GenerateTextOpts.RuntimeContext` (an `ai.RuntimeContext` map), read inside a tool's `Execute` (and inside `ApprovalRequirer.ApprovalRequired`/`ApproveToolCall`) via `ai.RuntimeContextFrom(ctx)`. Installed once per run, before the tool loop begins, so every step and resumed batch sees the same value. See [Tools § RuntimeContext](core/tools.md#runtimecontext). |
| Agents (`ToolLoopAgent` equivalent, agent-as-tool subagents) | **Shipped** — `agent.Agent` (`Generate`/`Stream`, `RunOpts`), `agent.AsTool` for agent-as-tool sub-agent delegation. Named plainly `Agent`, not `ToolLoopAgent` — see [Agents § Naming](core/agents.md#naming-toolloopagent-vs-agent). Contains no loop logic of its own; assembles a `GenerateTextOpts` and delegates entirely to `ai.GenerateText`/`ai.StreamText`. `WorkflowAgent` and a `toolOrder` hint are **v7-only** (see below), not modeled here. See [Agents](core/agents.md). |
| Tool-execution approvals (approval func, policy hook, resumable pending-approval flow) | **Shipped** — `ai.RequireApproval`/`ai.ApprovalRequirer`, `GenerateTextOpts.ApproveToolCall`/`.Approvals`, `GenerateTextResult.PendingApprovals`. Decision order is `Approvals` then `ApproveToolCall` then pending; a pending call suspends its whole batch atomically; denial is recorded as `*ai.ToolApprovalDeniedError` on an `IsError` tool result, never raised. Vercel models a pending approval as a special message part surfaced to a UI stream; `go-ai-sdk` has no UI layer, so it models the same idea as a suspended result (`PendingApprovals`) plus `Approvals` on the resume call instead — see [Tools § Approvals for tool execution](core/tools.md#approvals-for-tool-execution). A v7-redesigned approvals surface is **v7-only** (see below), not modeled here. |
| Sandbox interface / Code Mode | **Shipped** — `codemode.Tool(sandbox, tools, opts)` wraps a set of `ai.Tool`s into a single `run_code` tool; `codemode.Sandbox` is the interface the caller implements against their own runtime (subprocess, container, embedded interpreter) — the SDK ships no bundled code runtime, and security/isolation is entirely the sandbox implementer's responsibility. See [Code Mode](core/code-mode.md). |
| Video generation (`GenerateVideo`) | **Shipped** — `ai.GenerateVideo`, `provider.VideoModel`, `Registry.VideoModel`; Luma (Dream Machine, async poll), fal, and Replicate (both synchronous) implement it. See [Media § GenerateVideo](core/media.md#generatevideo). |
| Realtime/streaming transcription (`StreamTranscribe`, a minimal realtime voice session) | **Shipped**, over a stdlib-only WebSocket client (`internal/websocket`) — `ai.StreamTranscribe`/`provider.StreamingTranscriptionModel` (Deepgram live, OpenAI Realtime API in transcription mode); `(*openai.Provider).RealtimeSession` (OpenAI-only voice/text session, no generic provider interface, not wired into `ai.Registry`). Vercel's WebRTC realtime transport is out of scope (see below) — this SDK's realtime support is WebSocket-only. See [Media § StreamTranscribe](core/media.md#streamtranscribe) and [§ Realtime voice session](core/media.md#realtime-voice-session-openai-only). |
| Streaming audio translation (`StreamTranslate`) | **Not shipped.** None of the providers targeted so far expose a live/streaming audio-translation API (as distinct from streaming *transcription*, which is shipped — see above); `ai.Translate` (below) covers the REST translation use case instead. |
| Audio translation (`translate` / a translation model) | **Shipped as `ai.Translate` (REST), not `StreamTranslate`.** `ai.Translate`/`provider.TranslationModel`, OpenAI only (`internal/openaicompat.NewTranslationModel`, multipart `POST /audio/translations`, always English output regardless of source language). Not wired into `ai.Registry`. See [Media § Translate](core/media.md#translate). |
| File/skill upload (`uploadFile`, `uploadSkill`) | **Shipped.** `ai.UploadFile`/`ai.DeleteFile`/`provider.FileStore` (OpenAI, Anthropic — both with `files-api-2025-04-14`-equivalent beta gating on Anthropic's side), referenced from a prompt via the new `provider.FilePart.FileID`/`.URL` variants. `uploadSkill` is **Anthropic-only**: `(*anthropic.Provider).UploadSkill`/`.DeleteSkill`, a provider-specific capability with no generic interface (`anthropic-beta: skills-2025-10-02`). Neither is wired into `ai.Registry`. See [Media § Files & skills](core/media.md#files--skills). |
| MCP extensions (resources, prompts, completions, elicitation, token-provider auth) | **Shipped** — `Client.ListResources`/`ListResourceTemplates`/`ReadResource`, `ListPrompts`/`GetPrompt`, `Complete`, `SetElicitationHandler`/`ElicitationHandler` (server-initiated request dispatch), `NewStreamableHTTPTransportWithOptions` with `WithTokenProvider`/`WithAuthHeader`/`WithHTTPRetry`. **Caveat: elicitation over the HTTP transport is unsupported** — the Streamable HTTP transport has no server→client channel to receive a server-initiated request on, so `elicitation/create` can only reach the client over stdio today; see [MCP § Elicitation](mcp.md#elicitation) and [§ Limitations](mcp.md#limitations). Sampling and roots remain unimplemented. See [MCP § MCP scope](#mcp-scope) below. |
| Provider fleet (Moonshot, Qwen, MiniMax, DeepInfra, Hugging Face, Baseten, LM Studio, NVIDIA NIM, Voyage, Mixedbread, Cartesia, Prodia, Black Forest Labs, AI Gateway) | **Shipped** — 14 new provider packages, bringing the total to 39. See [Provider overview](providers/README.md). |
| OpenTelemetry bridge | **Shipped as `contrib/otel`** — a separate nested Go module (`github.com/azrtydxb/go-ai-sdk/contrib/otel`) implementing `ai.Telemetry` with real OpenTelemetry GenAI-semantic-convention spans (`gen_ai.operation.name`, `gen_ai.system`, `gen_ai.request.model`, `gen_ai.usage.*`, `gen_ai.response.finish_reasons`), nested so the root module stays zero-dependency. See [Telemetry § The contrib/otel bridge](core/telemetry.md#the-contribotel-bridge). |
| UI framework hooks (`useChat`/`useCompletion`/`useObject`, RSC streaming), MCP Apps rendering, DevTools/Terminal UI, WebRTC realtime transport | **Out of scope**, permanently — see [Features NOT ported](#features-not-ported) and the roadmap's standing scope rulings. |

### v7-only: confirmed not targeted

A handful of items visible on ai-sdk.dev as of the 2026-08-03 snapshot
belong to **AI SDK 7**, not 6, and are explicitly **not targeted** by this
port (tracked, not deferred — there is no "v6 gap" here to close):

- **`WorkflowAgent`** and a `toolOrder` execution-order hint on
  multi-tool steps.
- **`contextSchema`** — a schema attached to an agent/run's context object
  itself (distinct from `RuntimeContext`'s untyped bag).
- **A top-level `reasoning` enum** on a message/part (v6's `reasoning`
  option, which `go-ai-sdk` ships, is a *request*-side effort/budget knob
  — this is a different, response-shape-level enum).
- **A redesigned tool-execution approvals surface** (v7 reworks the
  approval message-part shape beyond what `go-ai-sdk`'s
  `PendingApprovals`/`Approvals` models today).

These will be revisited only if/when a v7 parity effort is chartered; see
the [v6 parity final audit](superpowers/specs/2026-08-03-v6-parity-final-audit.md)
for the full ruling.

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
| `generateVideo(opts)` (AI SDK 6) | `ai.GenerateVideo(ctx, ai.GenerateVideoOpts{...})` | Luma, fal, Replicate. |
| `transcribe(opts)` | `ai.Transcribe(ctx, ai.TranscribeOpts{...})` | |
| *(no direct equivalent — Vercel's realtime is WebRTC-based)* | `ai.StreamTranscribe(ctx, ai.StreamTranscribeOpts{...})` | Live bidirectional transcription over a stdlib WebSocket client; Deepgram, OpenAI. No retry (see [Media § StreamTranscribe](core/media.md#streamtranscribe)). |
| *(no direct equivalent)* | `ai.Translate(ctx, ai.TranslateOpts{...})` | English-only audio translation, OpenAI only; not `StreamTranslate` — see the [AI SDK 6 delta](#ai-sdk-6-delta) ruling above. |
| *(no direct equivalent)* | `ai.UploadFile`/`ai.DeleteFile(ctx, ...)` | `provider.FileStore`, OpenAI/Anthropic; referenced later via `provider.FilePart.FileID`. |
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
| `onAbort` | `OnAbort func()` | `StreamText`-only; see [Generating text § OnAbort](core/generating-text.md#onabort). |
| `activeTools` | `ActiveTools []string` | See the [ActiveTools resolution](#documented-divergences) divergence (#6). |
| `stopWhen: hasToolCall(name)` / a custom "loop finished" check | `ai.HasToolCall(names...)` / `ai.LoopFinished()` | Ready-made `StopWhen` helpers alongside `ai.StepCountIs`; see [Generating text § The multi-step tool loop](core/generating-text.md#the-multi-step-tool-loop). |
| `topK` / `presencePenalty` / `frequencyPenalty` / `seed` / `headers` | `TopK` / `PresencePenalty` / `FrequencyPenalty` / `Seed` / `Headers` (all pointers/maps, optional) | Per-provider support varies — see [Generating text § Additional call settings](core/generating-text.md#additional-call-settings-topk-penalties-seed-headers). |
| `timeout` (distinct connect/response/total timeouts) | `Timeout *ai.Timeout{Total, Step, Chunk}` | Not a field-for-field mapping — `go-ai-sdk`'s three dimensions are whole-run/per-step/stream-stall, not connect/response/total; see [Generating text § Timeout](core/generating-text.md#timeout-total-step-and-chunk). |
| `tool({ inputSchema, execute, strict, inputExamples })` (`strict`/`inputExamples` fields) | `ai.NewTool[Args](name, description, fn, ai.WithToolStrict(), ai.WithToolInputExamples(examples...))` | Trailing `ToolOption`s instead of object fields; see [Tools § Strict mode and input examples](core/tools.md#strict-mode-and-input-examples). |
| `tool({ onInputStart, onInputDelta, onInputAvailable })` | `ai.WithToolInputCallbacks(ai.ToolInputCallbacks{...})` | See [Tools § Per-tool input streaming hooks](core/tools.md#per-tool-input-streaming-hooks). |
| `extractJsonMiddleware()` | `ai.ExtractJSONMiddleware(model)` | See [Middleware and registry § ExtractJSONMiddleware](core/middleware-and-registry.md#extractjsonmiddleware). |
| `experimental_repairToolCall` | `RepairToolCall func(ctx, ai.ToolCallRecord, error) (ai.ToolCallRecord, bool)` | One retry, same as Vercel's documented behavior; Go signature returns `(call, ok bool)` instead of `Promise<ToolCall | null>`. |
| `maxRetries` | `MaxRetries *int` (pointer; `nil` = default 2) | |
| `maxOutputTokens` | `MaxTokens *int` | |
| `temperature` / `topP` / `stopSequences` | `Temperature` / `TopP` / `StopSequences` (all pointers where TS allows omission) | |
| `providerOptions` | `ProviderOptions map[string]any` | **Values are raw wire keys, not translated option names** — the single biggest divergence; see below. |
| `experimental_telemetry` (OTel-based) | `ai.TelemetryMiddleware(model, telemetryImpl)` + `ai.Telemetry` interface, or `ai.TelemetryMiddleware(model, otelbridge.New())` (`contrib/otel`) for real OpenTelemetry | See [Telemetry: a plain interface, plus a real OTel bridge](#telemetry-a-plain-interface-plus-a-real-otel-bridge). |
| MCP tools (`experimental_createMCPClient`) | `mcp.NewClient(transport)` + `mcp.Tools(ctx, client)` | Also covers resources, prompts, completions, and elicitation — see [MCP scope](#mcp-scope). |
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
| `extractJsonMiddleware()` | `ai.ExtractJSONMiddleware(model)` |
| A `tool()`-level `inputExamples` serialization helper for providers without native support | `ai.AddToolInputExamplesMiddleware(model)` |
| `wrapLanguageModel({ model, middleware: [a, b] })` | Compose by nesting: `ai.WrapModel(model, a)` then wrap again, or call `a(b(model))`-style directly since every middleware here is just `func(provider.LanguageModel) provider.LanguageModel` |
| Image-model middleware (`wrapImageModel` equivalent) | `ai.WrapImageModel(model, wrap)` — a naming hook only; no built-in image middlewares ship yet |

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

3. **Telemetry is a plain interface at the root; the OTel bridge is a
   separate module.** Vercel's `experimental_telemetry` is documented as
   built directly on OpenTelemetry conventions (span names, semantic
   attributes). The root `go-ai-sdk` module still ships zero external
   dependencies, so `ai.Telemetry` is a minimal `OnSpanStart`/`OnSpanEnd`
   seam — but as of wave 14, a real, ready-to-use OpenTelemetry bridge
   ships as [`contrib/otel`](../contrib/otel/README.md), a separate Go
   module implementing `ai.Telemetry` with GenAI-semconv spans. Use it
   directly (`otelbridge.New()`) instead of writing your own bridge,
   unless you need something OTel doesn't cover. See
   [Telemetry](core/telemetry.md) and
   [Telemetry: a plain interface, plus a real OTel bridge](#telemetry-a-plain-interface-plus-a-real-otel-bridge)
   below.

4. **MCP has no sampling/roots, and elicitation only works over stdio.**
   Vercel's `experimental_createMCPClient` is documented as supporting tools
   today with broader MCP surface evolving. `go-ai-sdk`'s `mcp` package
   covers tools, resources, resource templates, prompts, completions, and
   elicitation — but still has no `sampling/createMessage` or `roots/list`
   support, and elicitation (the one server-initiated request type
   implemented) can only reach the client over the stdio transport, since
   Streamable HTTP has no server→client channel to receive one on. See
   [MCP scope](#mcp-scope) below and [MCP](mcp.md).

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

### Telemetry: a plain interface, plus a real OTel bridge

```go
type Telemetry interface {
	OnSpanStart(ctx context.Context, info SpanInfo)
	OnSpanEnd(info SpanInfo)
}
```

**Breaking change (v0.2.0):** `OnSpanStart` now takes `ctx` as its first
argument, and `SpanInfo` gained `CorrelationID` — migrating a hand-rolled
`Telemetry` implementation is a one-line signature update (see
[Telemetry § Telemetry and SpanInfo](core/telemetry.md#telemetry-and-spaninfo)).
This is the only breaking change across the whole v6 parity program
(waves 9–14); pre-1.0, and the only known implementer was this doc's own
now-removed sketch.

For OpenTelemetry specifically, bridge it yourself (start a span in
`OnSpanStart`, end it in `OnSpanEnd`, keyed by `SpanInfo.CorrelationID`) —
or reach for the ready-made [`contrib/otel`](../contrib/otel/README.md)
bridge instead of writing one: `otelbridge.New()` returns an
`ai.Telemetry` that emits real GenAI-semconv spans. Full attribute mapping
and the stream-lifecycle span-ending rules are in
[Telemetry § The contrib/otel bridge](core/telemetry.md#the-contribotel-bridge).

### MCP scope

`mcp.Tools(ctx, client)` adapts an MCP server's tools into `[]ai.Tool` for
`GenerateTextOpts.Tools` — the original, still-primary way to consume an
MCP server. Beyond tools, the client also implements resources and resource
templates (`ListResources`/`ListResourceTemplates`/`ReadResource`), prompts
(`ListPrompts`/`GetPrompt`), argument completions (`Complete`), and
server-initiated elicitation (`SetElicitationHandler`); each of the
capability-gated methods returns a `*mcp.CapabilityError` rather than
sending a request if the server didn't advertise the matching capability.
There is still no equivalent of sampling or roots. Elicitation — the one
server-initiated request type implemented — only works over the stdio
transport: Streamable HTTP has no standalone server→client channel to
receive one on, so a server sending `elicitation/create` to an
HTTP-connected client gets no response. Full scope and known transport
deviations are in [MCP § Limitations](mcp.md#limitations).

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
  [`core/streaming.md#streampart-reference`](core/streaming.md#streampart-reference)
  and the grounding coverage in [`providers/google.md`](providers/google.md#quirks-and-notes)).
- **Provider-executed tools** (e.g. Anthropic/OpenAI server-side tools
  like web search or code execution that run on the provider's
  infrastructure rather than round-tripping through the caller) — not
  modeled; every tool call in `go-ai-sdk` today executes client-side via
  `ai.Tool.Execute`.
- **Partial-output streaming for `StreamText`'s `Output` modes** — the one
  remaining `StreamText`-side gap from the [output modes](#ai-sdk-6-delta)
  row above; `GenerateText`'s `Output` modes are fully shipped.

## Source of truth

- [`ai/options.go`](../ai/options.go), [`ai/generate_text.go`](../ai/generate_text.go),
  [`ai/stream_text.go`](../ai/stream_text.go) — `GenerateTextOpts` and the
  tool loop
- [`ai/output.go`](../ai/output.go) — `Output` modes on `GenerateText`
- [`ai/rerank.go`](../ai/rerank.go), [`provider/rerank.go`](../provider/rerank.go) —
  `ai.Rerank`/`provider.RerankingModel`
- [`ai/tool_result_content.go`](../ai/tool_result_content.go),
  [`ai/middleware_json.go`](../ai/middleware_json.go) — multi-modal tool
  results and `ExtractJSONMiddleware`
- [`ai/approval.go`](../ai/approval.go), [`ai/runtime_context.go`](../ai/runtime_context.go) —
  tool-execution approvals and `RuntimeContext`
- [`agent/`](../agent/) — `agent.Agent`, `agent.AsTool`
- [`codemode/`](../codemode/) — `codemode.Tool`, the `Sandbox` contract, `APIDoc`
- [`docs/core/`](core/) — the per-topic guides each divergence above is
  drawn from
- [`docs/mcp.md`](mcp.md) — MCP client scope and transport deviations
- [`docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md`](superpowers/plans/2026-08-03-v6-parity-roadmap.md) —
  the wave-by-wave AI SDK 6 parity plan the [delta table](#ai-sdk-6-delta)
  above is drawn from
- [`docs/superpowers/specs/2026-08-03-v6-parity-final-audit.md`](superpowers/specs/2026-08-03-v6-parity-final-audit.md) —
  the closing gap-audit record: the full have-list, deliberately-out-of-scope
  surface, and confirmed v7-only items
- [`contrib/otel/`](../contrib/otel/) — the OpenTelemetry bridge (separate
  Go module)
- [Architecture](architecture.md) — how the layers underneath these APIs
  fit together
