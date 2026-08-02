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
| 1 (v0.1) | OpenAI, Anthropic, Google (Gemini) | Shipped — three genuinely different wire formats prove the abstraction |
| 2 (v0.2) (shipped) | Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| 2 (v0.2) (shipped) | Mistral, Cohere | Own APIs, full provider implementations |
| 3 | Azure OpenAI, Vertex AI, Amazon Bedrock | Planned — platform auth; candidates for nested submodules |
| later | Image/speech/transcription providers | Out of scope for v1 — requires new model capability interfaces |

## Testing

- **Unit tests** per provider against `httptest` servers replaying recorded real-API fixtures (both non-streaming JSON and SSE transcripts).
- **Spec conformance suite**: a shared test harness (`provider/providertest`) that any `LanguageModel`/`EmbeddingModel` implementation runs — same matrix of prompt shapes, tool calls, streaming, and error cases across all providers. This is what keeps a 20-provider roster honest.
- **Core tests**: tool loop, retries, object decode/validation, batching — against an in-memory mock model.
- **Examples** in `examples/` are compiled in CI; ones needing live keys are gated behind env vars.
- Development follows TDD (red-green-refactor).

## Key decisions log

1. **Vercel AI SDK, not Google Vertex** — "vertex" in the original request meant Vercel; confirmed with user.
2. **Idiomatic Go over faithful TS mirror** — same concepts/naming, native Go expression.
3. **Single module** — submodules deferred until dependency bloat is demonstrated.
4. **All Vercel-supported providers as the end goal**, delivered in waves; v0.1 ships wave 1.
5. **Reflection-based JSON Schema from structs** replaces Zod for tools and structured output.
