# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it reaches 1.0.

## [Unreleased]

Wave 9 of the [AI SDK 6 parity roadmap](docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md):
v5 leftovers plus quick AI SDK 6 wins. Full parity with the AI SDK 5 core is
maintained; AI SDK 6 parity is in progress — see
[Migrating from the Vercel AI SDK § AI SDK 6 delta](docs/migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta)
for the feature-by-feature status.

### Added

- `GenerateTextOpts`/`provider.Call`: first-class `TopK`, `PresencePenalty`,
  `FrequencyPenalty`, and `Seed` sampling settings, threaded through to
  every language-model request path (per-provider support and wire-name
  mapping documented on each field), plus `Headers` for per-call extra HTTP
  headers (applied after — and never overriding — each provider's own
  auth header(s); SigV4-signed for Bedrock when the key starts with
  `x-amz-`).
- `ai.HasToolCall(names ...string)` and `ai.LoopFinished()`: two more
  ready-made `StopWhen` helpers alongside `ai.StepCountIs`.
- `GenerateTextOpts.OnAbort`: fires exactly once per `TextStream` when the
  consumer abandons `Parts()` iteration early or the call context is
  canceled mid-stream — mutually exclusive with `OnFinish`/`OnError` for
  the same event.
- `ai.ToolResultContent`: multi-modal tool results — a `Tool.Execute` can
  return text plus one or more `provider.GeneratedImage`s. Serialized
  natively by anthropic and bedrock; text-projected (images dropped) by
  openaicompat-based providers, geminicompat, cohere, and mistral.
- `ai.ExtractJSONMiddleware`: strips markdown code fences (` ```json `)
  from a model's text output, both `Generate` and incrementally in
  `Stream`, reusing `GenerateObject`'s fence-stripping rule.
- `ai.WrapImageModel`: the `provider.ImageModel` counterpart to
  `ai.WrapModel` — a one-line naming hook for image-model middleware.
- AssemblyAI, Gladia, and Rev.ai `provider.TranscriptionModel`
  implementations (`providers/assemblyai`, `providers/gladia`,
  `providers/revai`), each an asynchronous upload/create-then-poll flow
  with `WithPollInterval`-controlled, ctx-aware polling — bringing the
  provider total to 25.

### Changed

- `GenerateTextOpts.StopWhen` is now consulted after **every** completed
  step, not only steps that requested tool calls — this removes the
  previously-documented divergence from Vercel's `stopWhen`, which is
  consulted after every step too. A step with no tool calls still always
  ends the loop naturally regardless of what `StopWhen` returns for it.

### Fixed

- `providers/revai`: documented that `"unknown"`-type transcript elements
  (unintelligible speech) are intentionally omitted from both `Text` and
  `Segments`.
- `providers/gladia`: documented that the job-creation response's
  `result_url` is intentionally unused — this provider polls by job `id`
  instead.
- `providers/assemblyai`: a terminal `"error"` transcript status with an
  empty `error` field now includes the raw response body in the returned
  error, instead of reporting the failure with no detail at all.

## [0.1.0] — 2026-08-03

The public API described in the [design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md)
is implemented and tested end-to-end: the full core SDK, 22 providers, media
capabilities, and the parity features listed below. Providers are verified
against recorded/documented wire formats via fixture tests; none have been
smoke-tested against live APIs yet (see the
[live-testing status](docs/providers/README.md#live-testing-status)).

### Added

**Core SDK**

- `ai.GenerateText` / `ai.StreamText`: text generation with an automatic
  multi-step tool-calling loop (`MaxSteps`, `StopWhen`, `ai.StepCountIs`,
  `PrepareStep`, `OnStepFinish`), conversation continuation via
  `GenerateTextResult.Messages`, and a shared retry wrapper
  (`MaxRetries`, `*ai.RetryError`).
- `ai.GenerateObject[T]` / `ai.StreamObject[T]`: structured output with a
  JSON Schema derived from Go structs by reflection, native-JSON mode
  where a provider supports it and forced-tool-call mode otherwise.
- `ai.Embed` / `ai.EmbedMany`: single and batched embeddings, with
  automatic batching against each provider's `MaxBatchSize()`, and
  `ai.CosineSimilarity` for comparing vectors.
- `ai.NewTool[Args]`: typed tool definitions with a reflection-derived
  JSON Schema; the tool loop's error taxonomy
  (`*ai.NoSuchToolError`, `*ai.InvalidToolArgumentsError`,
  `*ai.ToolExecutionError`) and `RepairToolCall` (one retry at fixing a
  failing call before the normal error path).
- `ai.ActiveTools`: restricts which of `Tools` are offered to the model
  and treated as known during execution.
- Stream lifecycle callbacks: `OnChunk`, `OnFinish`, `OnError` (with the
  argument-validation exclusion rule) on both `GenerateText` and
  `StreamText`.
- `ai.SmoothStream`: re-chunks `TextDelta`s into word- or line-granularity
  pieces for a steadier UI cadence, with no implicit default delay.
- Reasoning/thinking content: `provider.ReasoningPart` (non-streaming),
  `provider.ReasoningDelta`/`ReasoningEnd` (streaming), and
  `Result.ReasoningText`/`Stream.ReasoningText()`.
- Sources/grounding: `provider.SourcePart` and `provider.SourceEvent`
  (Google `groundingMetadata` today).
- `provider.ProviderOptions` / `ProviderMetadata`: the namespaced,
  raw-wire-key escape hatch for provider-specific request parameters and
  response data, threaded through `Call`, `Response`, and `FinishPart`.
- Middleware: `ai.ExtractReasoningMiddleware`, `ai.SimulateStreamingMiddleware`,
  `ai.DefaultSettingsMiddleware`, `ai.WrapModel`.
- `ai.Registry`: resolves `"provider:model"` strings into concrete
  language, embedding, image, speech, and transcription models.
- `ai.Telemetry` / `ai.TelemetryMiddleware`: a minimal, dependency-free
  span-reporting seam (`OnSpanStart`/`OnSpanEnd`) for bridging to
  OpenTelemetry or any other tracing system.
- File attachments: `provider.FilePart{Data, MediaType, Filename}` for
  user messages, with provider-specific support (PDF-only, any media
  type, a fixed document-type set, or unsupported, depending on the
  provider).
- `provider.ProviderMetadata` population from every provider that reports
  extra response data (Anthropic prompt-cache token counts,
  `openaicompat`'s `system_fingerprint`).

**Providers (22)**

- Wave 1 — OpenAI, Anthropic, Google (Gemini): full, independent
  implementations proving the provider abstraction across three distinct
  wire formats.
- Wave 2 — Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity
  as thin presets over the extracted `internal/openaicompat` base; Mistral
  and Cohere as full, standalone implementations.
- Wave 3 — Azure OpenAI (API-key preset over `internal/openaicompat`),
  Vertex AI (Google service-account/ADC auth, `internal/geminicompat`
  extracted from the `google` provider), Amazon Bedrock (Converse API,
  AWS SigV4 request signing, `eventstream` framing).
- Each provider implements `provider.LanguageModel` and, where the
  vendor's API supports it, `provider.EmbeddingModel`; all pass the
  shared `provider/providertest` conformance suite.

**Media capabilities**

- `ai.GenerateImage`, `ai.GenerateSpeech`, `ai.Transcribe` entry points
  and their `provider.ImageModel` / `SpeechModel` / `TranscriptionModel`
  interfaces.
- OpenAI (image, speech, transcription), Google/Vertex AI (Imagen image
  generation), xAI (image), Groq (transcription), and ElevenLabs (speech
  + transcription) implementations.
- Wave 8 — fal and Replicate (synchronous image generation), Luma
  (asynchronous, poll-until-terminal image generation via
  `WithPollInterval`), Deepgram (transcription, raw-audio request body),
  and LMNT and Hume (speech synthesis), closing out the Vercel-supported
  media roster targeted for v0.1. All six are implemented against
  documented wire formats and covered by fixture-based unit tests, but
  not yet smoke-tested against the live APIs — see
  [Provider overview: Live-testing status](docs/providers/README.md#live-testing-status).

**Core parity features**

- `provider.ToolChoice`, `Call.Temperature`/`TopP`/`StopSequences`, usage
  detail fields (cached/reasoning tokens).
- `ai.DefaultSettingsMiddleware` provider-options merge semantics.
- Provider-metadata plumbing through both `Generate` and `Stream` paths.

**MCP (Model Context Protocol)**

- Package `mcp`: a v1, tools-only MCP client over stdio
  (`mcp.NewStdioTransport`) and Streamable HTTP
  (`mcp.NewStreamableHTTPTransport`) transports, pinned to protocol
  version `2025-03-26`.
- `mcp.Tools(ctx, client)`: adapts a server's tools into `[]ai.Tool` for
  direct use in `GenerateTextOpts.Tools`, including `IsError` tool
  results surfacing as `*ai.ToolExecutionError`.

**Documentation**

- Getting-started guide, per-topic core guides (`generating-text`,
  `tools`, `structured-output`, `embeddings`, `media`, `streaming`,
  `reasoning`, `middleware-and-registry`, `provider-options`,
  `errors-and-retries`, `telemetry`), MCP guide, and a full
  provider-reference page per vendor.
- [Migrating from the Vercel AI SDK](docs/migrating-from-vercel-ai-sdk.md)
  and [Architecture](docs/architecture.md).

[Unreleased]: https://github.com/azrtydxb/go-ai-sdk/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/azrtydxb/go-ai-sdk/releases/tag/v0.1.0
