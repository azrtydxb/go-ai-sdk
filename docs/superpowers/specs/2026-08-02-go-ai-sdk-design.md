# go-ai-sdk — Design Spec

**Date:** 2026-08-02
**Status:** Approved
**Module:** `github.com/azrtydxb/go-ai-sdk`
**Go version:** 1.26+

## Summary

An idiomatic Go port of the [Vercel AI SDK](https://sdk.vercel.ai) (the TypeScript `ai` package). It keeps the AI SDK's concepts and architecture — a unified core API over a provider abstraction — but expresses them in native Go: `context.Context` first arguments, config structs, `iter.Seq` streams, generics for typed tools and structured output, and `errors.As`-able typed errors.

## Goals (v0.1)

- **Core text**: `GenerateText`, `StreamText` over a provider abstraction.
- **Tool calling**: typed tool definitions, automatic multi-step tool-call loop (`MaxSteps`).
- **Structured output**: `GenerateObject[T]`, `StreamObject[T]` with JSON Schema derived from Go structs.
- **Embeddings**: `Embed`, `EmbedMany` with automatic batching.
- **Providers, wave 1**: OpenAI, Anthropic, Google (Gemini API). Later waves add the rest of the Vercel-supported roster (see Provider Waves).

## Non-goals (v0.1)

- Image generation, speech, and transcription models (and their providers: Fal, Replicate, ElevenLabs, etc.). The provider spec may reserve room for them, but they are not in this spec.
- UI/framework helpers (`useChat`, RSC) — no Go equivalent; out of scope permanently.
- Agent abstractions beyond the multi-step tool loop.

## Architecture

Single Go module, three layers:

```
github.com/azrtydxb/go-ai-sdk
├── go.mod
├── ai/          High-level API: GenerateText, StreamText, GenerateObject, Embed, tools
├── provider/    The spec: interfaces + unified types every provider implements
├── providers/
│   ├── openai/        also the base for OpenAI-compatible endpoints
│   ├── anthropic/
│   ├── google/        Gemini API
│   └── ...            later waves
├── internal/    shared plumbing (SSE parsing, retry, JSON Schema reflection)
└── examples/    runnable example programs per feature
```

Heavy-dependency providers (Bedrock → AWS SDK, Vertex → Google auth) may later be split into nested submodules if dependency bloat becomes a real problem; this is explicitly deferred.

### Layer 1: `provider` (the spec)

Go equivalent of `@ai-sdk/provider`'s `LanguageModelV2` spec. Contains only interfaces and unified types — no orchestration logic.

```go
type LanguageModel interface {
    Generate(ctx context.Context, call Call) (*Response, error)
    Stream(ctx context.Context, call Call) (StreamResponse, error)
    ModelID() string
    ProviderName() string
}

type EmbeddingModel interface {
    Embed(ctx context.Context, values []string) (*EmbeddingResponse, error)
    MaxBatchSize() int
    ModelID() string
    ProviderName() string
}
```

Unified types:

- **Prompt**: `[]Message` with roles system / user / assistant / tool.
- **Content parts**: text, image, file, tool call, tool result. Assistant and user messages hold `[]ContentPart`.
- **Call options** (`Call`): messages, tool definitions (name / description / JSON Schema), tool choice, temperature, max tokens, top-p, stop sequences, response-format (for JSON mode), provider-specific options escape hatch (`map[string]any`).
- **Response**: content parts, `FinishReason` (stop / length / tool-calls / content-filter / error), `Usage` (input/output/total tokens), raw provider response metadata.
- **StreamPart** (sealed interface): `TextDelta`, `ToolCallDelta`, `ToolCall` (complete), `FinishPart` (reason + usage), `ErrorPart`.

Providers translate between this contract and their wire format. Nothing else. All orchestration (tool loops, retries, object parsing, batching) lives once in `ai`.

**Middleware** comes free: because `LanguageModel` is an interface, wrappers (logging, caching, prompt rewriting — the TS SDK's `wrapLanguageModel`) are plain interface decorators. `ai` ships a `WrapModel` helper but no built-in middlewares in v0.1.

### Layer 2: `ai` (core API)

```go
model := anthropic.New().Model("claude-sonnet-5")
// provider constructors take functional options: WithAPIKey, WithBaseURL, WithHTTPClient

res, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
    Model:    model,
    Prompt:   "...",            // or Messages: []ai.Message{...} (mutually exclusive)
    System:   "...",
    Tools:    []ai.Tool{weather},
    MaxSteps: 5,                // default 1
    // MaxTokens, Temperature (*float64), TopP, StopSequences, MaxRetries...
})
// res.Text, res.ToolCalls, res.ToolResults, res.Steps, res.FinishReason, res.Usage

stream, err := ai.StreamText(ctx, opts)     // error = failed to start (auth, connect)
for part := range stream.Parts() {          // iter.Seq[StreamPart], single use
    if d, ok := part.(ai.TextDelta); ok { fmt.Print(d.Text) }
}
if err := stream.Err(); err != nil { ... }  // bufio.Scanner pattern
// after the loop: stream.Text(), stream.Usage(), stream.FinishReason()
```

- **Tool loop**: when the model returns tool calls and `MaxSteps` allows, `ai` executes the tools, appends results as tool messages, and calls the model again. Each round is recorded in `res.Steps`. `StreamText` runs the same loop; tool activity surfaces as stream parts.
- **Tools**: `ai.NewTool[Args](name, description, fn)` where `fn = func(ctx context.Context, args Args) (any, error)`. `Args` is a struct; its JSON Schema is derived via reflection from field types and struct tags (`json`, `jsonschema:"description=...,enum=..."`). This is Go's stand-in for Zod.
- **Structured output**: `ai.GenerateObject[T](ctx, opts)` derives the schema from `T` the same way, requests JSON output (native JSON mode where the provider supports it, else tool-based extraction — chosen per provider capability flags), then decodes and validates into `T`. Failure returns `NoObjectGeneratedError` carrying the raw text. `StreamObject[T]` streams partial JSON and yields best-effort partial values.
- **Embeddings**: `ai.Embed(ctx, EmbedOpts)` (single value) and `ai.EmbedMany` (splits input into batches of `MaxBatchSize`, runs them, reassembles in order).
- **Retries**: exponential backoff with jitter on retryable errors (429, 5xx, network), default 2 retries, configurable via `MaxRetries`. Implemented once in `ai`/`internal`, never in providers.

### Layer 3: `providers/*`

Each provider package exports `New(opts ...Option) *Provider` with `Model(id string) provider.LanguageModel` and (where supported) `EmbeddingModel(id string) provider.EmbeddingModel`. API keys default to the conventional env vars (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_GENERATIVE_AI_API_KEY`).

Responsibilities: unified prompt → wire format, wire response/SSE → unified types, provider-specific error mapping to `APICallError`. The `openai` package doubles as the OpenAI-compatible base: wave-2 clones are thin presets over it.

## Error handling

Typed errors in `ai`, matched with `errors.As`/`errors.Is`:

- `APICallError` — status code, response body, request URL, `Retryable` flag.
- `NoObjectGeneratedError` — raw model text included.
- `InvalidToolArgumentsError`, `NoSuchToolError`, `ToolExecutionError` (wraps the tool's own error).
- `RetryError` — wraps the last error after retries are exhausted.

Context cancellation propagates everywhere; a canceled stream ends iteration and reports `ctx.Err()` via `stream.Err()`.

## Streaming internals

Shared SSE parser in `internal/sse` (handles `data:` framing, `[DONE]`, comment keep-alives). Each provider adapts its event scheme into unified `StreamPart`s. Backpressure is natural: parts are produced as the consumer iterates (pull-based `iter.Seq`); abandoning the loop early closes the underlying response body.

## Provider waves

| Wave | Providers | Status |
|---|---|---|
| 1 | OpenAI, Anthropic, Google (Gemini) | Shipped — three genuinely different wire formats prove the abstraction |
| 2 (shipped) | Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| 2 (shipped) | Mistral, Cohere | Own APIs, full provider implementations |
| 3 (shipped) | Azure OpenAI, Vertex AI, Amazon Bedrock | Platform auth: Azure (API-key preset over the OpenAI-compatible base), Vertex AI (Google service-account/ADC auth), Bedrock (AWS SigV4 request signing); no submodule split was needed |
| later | Image/speech/transcription providers | Out of scope for v1 — requires new model capability interfaces |

## Testing

- **Unit tests** per provider against `httptest` servers replaying recorded real-API fixtures (both non-streaming JSON and SSE transcripts).
- **Spec conformance suite**: a shared test harness (`provider/providertest`) that any `LanguageModel`/`EmbeddingModel` implementation runs — same matrix of prompt shapes, tool calls, streaming, and error cases across all providers. This is what keeps a 20-provider roster honest.
- **Core tests**: tool loop, retries, object decode/validation, batching — against an in-memory mock model.
- **Examples** in `examples/` are compiled in CI; ones needing live keys are gated behind env vars.
- Development follows TDD (red-green-refactor).

## Capability extension (wave 4, shipped)

Wave 4 extends the core API with image generation, speech synthesis, and audio transcription capabilities, following the same pattern as text and embeddings.

### Provider interfaces

Three new interfaces parallel `LanguageModel` and `EmbeddingModel`:

```go
type ImageModel interface {
    GenerateImages(ctx context.Context, call ImageCall) (*ImageResponse, error)
    ModelID() string
    ProviderName() string
}

type SpeechModel interface {
    GenerateSpeech(ctx context.Context, call SpeechCall) (*SpeechResponse, error)
    ModelID() string
    ProviderName() string
}

type TranscriptionModel interface {
    Transcribe(ctx context.Context, call TranscriptionCall) (*TranscriptionResponse, error)
    ModelID() string
    ProviderName() string
}
```

Unified types:
- `ImageCall`: prompt, N (count), size, aspect ratio, seed.
- `SpeechCall`: text, voice, output format, speed, language.
- `TranscriptionCall`: audio bytes, media type, language, prompt (for context).
- Responses carry the generated media (raw bytes), media type, and metadata (language, duration).

### Core API functions

- `ai.GenerateImage(ctx, GenerateImageOpts)` — generates one or more images from a text prompt.
- `ai.GenerateSpeech(ctx, GenerateSpeechOpts)` — synthesizes speech audio from text.
- `ai.Transcribe(ctx, TranscribeOpts)` — transcribes audio to text.

Each follows the established pattern: validates required fields, wraps the call in retry logic (default 2 retries), and returns a result struct or typed error.

### Provider coverage

| Capability | OpenAI | Google | Vertex AI | xAI | ElevenLabs | Groq |
|---|---|---|---|---|---|---|
| `GenerateImage` | ✅ | ✅ | ✅ | ✅ | — | — |
| `GenerateSpeech` | ✅ | — | — | — | ✅ | — |
| `Transcribe` | ✅ | — | — | — | ✅ | ✅ |

**Shared implementation:** OpenAI-compatible providers (groq, xai) reuse the `openaicompat` helpers introduced for text. Google-based providers (vertex) reuse the `geminicompat` base. ElevenLabs gets its own full implementation.

## Core parity (wave 5, shipped)

Wave 5 closes most of the remaining Vercel AI SDK core-parity gap: provider
options as a documented first-class convention, reasoning/thinking content
and detailed token usage, `wrapLanguageModel`-style middlewares, a provider
registry, tool-loop stop/prepare/finish hooks, `smoothStream`, grounding
sources, and `cosineSimilarity`.

### ProviderOptions convention

`provider.Call.ProviderOptions` (`map[string]any`, threaded through
unchanged from `ai.GenerateTextOpts.ProviderOptions` and the equivalent
image/speech/transcription opts) is keyed by provider name — the value
returned by the model's `ProviderName()`. Each provider looks up its own
key only; the value under a matching key must itself be a `map[string]any`,
shallow-merged into the built request body after the SDK constructs it, so
option entries win over SDK-set fields. Novel keys not otherwise exposed by
the SDK pass through untouched. For multipart-body calls (openaicompat/
ElevenLabs transcription), entries are sent as extra form fields instead.

### Reasoning and detailed usage

`provider.ReasoningPart` (non-streaming) and `provider.ReasoningDelta` /
`provider.ReasoningEnd` (streaming) carry reasoning/thinking content
uniformly across providers; `Response.ReasoningText()` /
`TextStream.ReasoningText()` give the concatenated text.
`ReasoningPart.Signature` and `.Redacted` are Anthropic-specific: extended
thinking is opted into per call via
`ProviderOptions["anthropic"]["thinking"]`
(`map[string]any{"type": "enabled", "budget_tokens": N}`); redacted
thinking blocks set `Redacted` true with the opaque payload in `Text`, and
a visible block's cryptographic `Signature` is preserved so it round-trips
back to the API on a later turn (this package automatically reorders
`ReasoningPart`s to lead the assistant message content, as the Messages API
requires).

`provider.Usage` grew two fields beyond the input/output/total token
counts: `CachedInputTokens` (prompt-cache hits — Anthropic
`cache_read_input_tokens`, OpenAI-compatible
`usage.prompt_tokens_details.cached_tokens`) and `ReasoningTokens` (tokens
spent on reasoning), both zero when a provider doesn't report them.

### Middlewares

Three `provider.LanguageModel` decorators in `ai`, mirroring the TS SDK's
`wrapLanguageModel` built-ins:

- `ExtractReasoningMiddleware(model, ExtractReasoningOpts{TagName, StartWithReasoning})`
  splits `<tag>...</tag>` spans out of text output into reasoning content,
  for models that signal thinking with an inline tag (e.g. some
  DeepSeek-compatible endpoints) rather than a dedicated content type. The
  stream path is fully incremental — it never buffers more than the
  longest unresolved tag-prefix candidate.
- `SimulateStreamingMiddleware(model)` makes `Stream` call `Generate` and
  replay the result as a synthetic single-shot stream, for models/providers
  that only support non-streaming calls.
- `DefaultSettingsMiddleware(model, defaults)` fills in zero-valued call
  fields (`Temperature`, `TopP`, `MaxTokens`, `StopSequences`,
  `ProviderOptions`) from `defaults`; `ProviderOptions` merges per
  provider-name namespace with per-call entries winning.

### Registry

`ai.Registry` (`NewRegistry`, `Register(name string, p any)`) resolves
`"provider:model"` strings into concrete models. `Register` accepts any
value; each lookup method (`LanguageModel`, `EmbeddingModel`, `ImageModel`,
`SpeechModel`, `TranscriptionModel`) type-asserts the registered provider
against the matching capability interface (`LanguageModelProvider`,
`EmbeddingModelProvider`, ...) at lookup time, so a provider need not
implement every capability. The model id is split on the first `:` so
model ids that themselves contain `:` (e.g. Bedrock's
`anthropic.claude-3:1`) round-trip intact.

### Tool-loop controls

`GenerateTextOpts` gained three fields, shared by `GenerateText` and
`StreamText`:

- `StopWhen func(steps []Step) bool` — evaluated after each completed step
  that requested tool calls (a step with no tool calls always ends the loop
  naturally); returning true stops the loop. `ai.StepCountIs(n)` is the
  built-in "stop once len(steps) >= n" helper. If `MaxSteps` is unset (0)
  and `StopWhen` is non-nil, the hard step cap defaults to 16 instead of 1.
- `PrepareStep func(stepIndex int, plan StepPlan) (StepPlan, bool)` — called
  before each model call with `StepPlan{Call, Model}` (`Model` is the model
  that will make the call: `opts.Model` on step 0, or whatever a prior
  `PrepareStep` call last swapped to); returning `(plan, true)` substitutes
  the returned `StepPlan` for that step. `StepPlan.Model` is nil-means-keep:
  setting it swaps the model used for that step's call AND every step after
  it, until `PrepareStep` swaps again — the swap persists rather than
  applying to a single step.
- `OnStepFinish func(step Step)` — called after each step completes
  (including the final step). In `StreamText`, this fires only once a
  step's `Parts()` iteration has run to completion; if the consumer stops
  ranging over `Parts()` early (e.g. breaking right after that step's
  `FinishPart`), `OnStepFinish` does not fire for that step even though
  `FinishPart` was already delivered.

### SmoothStream

`ai.SmoothStream(parts iter.Seq[provider.StreamPart], SmoothOpts{Chunking, Delay})`
re-chunks `TextDelta`s into complete "word + trailing whitespace" (default)
or "line + trailing newline" units, optionally sleeping `Delay` between
emitted parts for a steadier UI cadence. Unlike the TS SDK's
`smoothStream`, there is no implicit default delay — `Delay: 0` (the zero
value) means no delay at all, keeping behavior deterministic. Only
`TextDelta` is re-chunked; every other `StreamPart` (including
`ReasoningDelta`) passes through untouched, after flushing any
currently-buffered text first.

### Sources

`provider.SourcePart` (`ID`, `URL`, `Title`) is a citation/grounding
content part, and `provider.SourceEvent` carries one mid-stream. Currently
only `geminicompat` populates it, from Google's
`groundingMetadata.groundingChunks`; `result.Sources` /
`stream.Sources()` surface the accumulated set. Anthropic's citations are
documented as future work, not covered in this wave.

### CosineSimilarity

`ai.CosineSimilarity(a, b []float64) (float64, error)` computes
`dot(a, b) / (||a|| * ||b||)`, erroring on mismatched lengths or a
zero-magnitude vector (undefined cosine similarity).

### Other changes

- `provider.EmbeddingModelWithOptions` (previously `EmbeddingModelV2`,
  renamed pre-1.0 to avoid implying interface versioning) is the optional
  `EmbeddingModel` extension for models that support per-call
  `ProviderOptions`, via `EmbedCall(ctx, EmbeddingCall) (*EmbeddingResponse, error)`.
- `internal/imagesniff` centralizes the magic-byte MediaType sniffer
  previously duplicated in `internal/openaicompat` and
  `internal/geminicompat`'s image models.

## Wave 6 (shipped)

Wave 6 closes the remaining core-parity gaps for wave 1's target scope: an
MCP client and tool adapter, telemetry spans, stream lifecycle callbacks,
tool-call repair and active-tool filtering, file-attachment content parts,
and a provider-metadata escape hatch on responses.

### MCP client (`mcp`)

New package `mcp` implements a client for the [Model Context
Protocol](https://modelcontextprotocol.io) — the OTel-free analog of the TS
SDK's `experimental_createMCPClient`:

- `mcp.NewClient(t Transport) *Client` wraps a `Transport` with the JSON-RPC
  request/response bookkeeping (`call`, `notify`, a `recvLoop` that
  demultiplexes replies by id). `Client.Initialize(ctx)` performs the MCP
  handshake (`initialize` request, then a `notifications/initialized`
  notification). `Client.Close()` shuts down the transport.
- Two `Transport` implementations: `mcp.NewStdioTransport(cmd []string, env
  []string) (Transport, error)` launches `cmd` as a child process and speaks
  newline-delimited JSON-RPC over its stdin/stdout (stderr passed through to
  the parent's); `mcp.NewStreamableHTTPTransport(url string, headers
  map[string]string) Transport` speaks the MCP Streamable HTTP transport
  (POST per message, response either a direct JSON body or an SSE stream,
  session id captured from `Mcp-Session-Id` and echoed on later requests).
- `Client.ListTools(ctx) ([]ToolDef, error)` calls `tools/list`,
  transparently paginating via `nextCursor`. `Client.CallTool(ctx, name,
  args) (*ToolResult, error)` calls `tools/call`, concatenating `"text"`
  content parts into `ToolResult.Text` (other content types, e.g. images,
  are ignored in v1).
- `mcp.Tools(ctx, client) ([]ai.Tool, error)` adapts every tool the server
  lists into an `ai.Tool` whose `Execute` forwards raw JSON arguments to
  `CallTool` verbatim (schema is not re-derived) and turns
  `ToolResult.IsError == true` into a Go error, so the `ai` tool loop
  records it as a failed tool call. The returned slice drops straight into
  `GenerateTextOpts.Tools` / `StreamText`, closing a tool loop over an
  external MCP server in four calls:

  ```go
  transport, _ := mcp.NewStdioTransport([]string{"my-mcp-server"}, nil)
  client := mcp.NewClient(transport)
  defer client.Close()
  client.Initialize(ctx)
  tools, _ := mcp.Tools(ctx, client)

  result, _ := ai.GenerateText(ctx, ai.GenerateTextOpts{
      Model: model, Prompt: "...", Tools: tools, MaxSteps: 3,
  })
  ```

  See `examples/mcp-tools/main.go` for a complete, env-guarded runnable
  version (argv[1:] is the server command).

### Telemetry (OTel-free analog)

`ai.TelemetryMiddleware(model, t Telemetry) provider.LanguageModel` wraps a
model so every `Generate`/`Stream` call reports a span
(`SpanInfo{Operation, ModelID, ProviderName, StartTime, EndTime, Usage,
FinishReason, Err}`) to a user-supplied `Telemetry` (`OnSpanStart`,
`OnSpanEnd`). This is a deliberate, documented divergence from the TS SDK's
`experimental_telemetry`, which integrates directly with OpenTelemetry:
`go-ai-sdk` ships no OTel dependency (stdlib-only constraint), so
`Telemetry` is a minimal seam an application wires to OTel itself (start a
span in `OnSpanStart`, stash it, end it in `OnSpanEnd`) or to any other
sink. `Generate` emits exactly one span; `Stream` emits one span that ends
at the stream's `FinishPart` (or, failing that, whenever `Parts()` iteration
stops for any other reason — a mid-stream error, early abandonment, or
`Close`) — in every case ending with whatever is known at that point, never
inventing usage/finish data for an abandoned or errored stream.

### Stream lifecycle callbacks

`GenerateTextOpts` gained three callbacks, effective in both `GenerateText`
and `StreamText`:

- `OnChunk func(part provider.StreamPart)` — called with each stream part
  before it reaches the consumer (`StreamText` only; `GenerateText` has no
  part stream to observe).
- `OnFinish func(result *GenerateTextResult)` — called once on successful
  completion: right before `GenerateText` returns, or at `StreamText`'s
  natural end-of-loop (never on an error, and never if the consumer
  abandons iteration early).
- `OnError func(err error)` — called with a call's terminal error in both
  APIs (for `GenerateText`, in addition to — not instead of — the returned
  error, so one callback can serve both APIs uniformly); not invoked for
  argument-validation failures (nil `Model`, bad `Prompt`/`Messages`
  combination), which are reported solely via the returned error.

### RepairToolCall and ActiveTools

Two more `GenerateTextOpts` fields, also shared by `GenerateText` and
`StreamText`:

- `ActiveTools []string` limits which of `Tools` are offered to the model
  (filters the `ToolDef`s built into the `Call`) and, independently,
  restricts execution: a call naming a tool outside the active set is
  treated as unknown (`*NoSuchToolError`) even if it's present in `Tools`.
  `nil` means every tool is active.
- `RepairToolCall func(ctx, call ToolCallRecord, toolErr error)
  (ToolCallRecord, bool)` is invoked when a tool call fails to validate — an
  unknown name or an `*InvalidToolArgumentsError` from `Execute` — and may
  return a corrected call, retried once. If the repaired call fails again,
  `RepairToolCall` is not invoked a second time for that original call, and
  the normal error path (abort the batch / record the error) applies.

### FilePart

`provider.FilePart{Data, MediaType, Filename}` is a new user-message
content part for file attachments, alongside `TextPart`/`ImagePart`.
Support is intentionally uneven across providers (documented on the type
itself, `provider/message.go`):

| Provider(s) | Support |
|---|---|
| anthropic | `application/pdf` only, sent as a `"document"` content block |
| google, vertex (`geminicompat`) | any `MediaType`, sent inline via `inlineData` |
| openai + `openaicompat` presets (azure, cerebras, deepseek, fireworks, groq, perplexity, together, xai) | `application/pdf` only, sent as a `"file"` content part with a `data:` URL — OpenAI itself is confirmed to accept it; other OpenAI-compatible servers may reject it |
| cohere, mistral, bedrock | unsupported; a `FilePart` in a user message returns a descriptive error |

### ProviderMetadata

`provider.Response` gained `ProviderMetadata map[string]any` — the response
analog of `Call.ProviderOptions`, namespaced by provider name, `nil` when a
provider has nothing to report. Two providers populate it in this wave:

- `anthropic`: `ProviderMetadata["anthropic"]["cache_creation_input_tokens"]`
  when the Messages API usage block reports a non-zero
  `cache_creation_input_tokens` (tokens newly written to the prompt cache,
  as opposed to `Usage.CachedInputTokens`, which tracks cache *reads*).
- `openaicompat` (every preset built on it): `ProviderMetadata["<cfg.Name>"]["system_fingerprint"]`
  when the response carries a non-empty `system_fingerprint`.

### Remaining gaps (explicitly out of scope)

Documented here rather than silently dropped: Anthropic citations/source
parts (Google-only today, via `geminicompat`'s `groundingMetadata`),
provider-executed tools (server-side tool execution some providers offer),
and a native OpenTelemetry exporter (superseded by the minimal `Telemetry`
seam above, which an application can bridge to OTel itself).

## Key decisions log

1. **Vercel AI SDK, not Google Vertex** — "vertex" in the original request meant Vercel; confirmed with user.
2. **Idiomatic Go over faithful TS mirror** — same concepts/naming, native Go expression.
3. **Single module** — submodules deferred until dependency bloat is demonstrated.
4. **All Vercel-supported providers as the end goal**, delivered in waves; v0.1 ships wave 1.
5. **Reflection-based JSON Schema from structs** replaces Zod for tools and structured output.
6. **Wave 4 media interfaces** — new top-level interfaces for image/speech/transcription (not embedded in LanguageModel) to keep the contract simple and separate concerns. Providers implement one, some, or all capabilities; the capability matrix is the source of truth.
