# go-ai-sdk

An idiomatic Go port of the [Vercel AI SDK](https://sdk.vercel.ai): a single,
provider-agnostic API for generating text, streaming text, generating
structured objects, calling tools, computing embeddings, and generating
images/speech/transcriptions across **25 providers** — OpenAI, Anthropic,
Google (Gemini), Groq, xAI, DeepSeek, Together, Fireworks, Cerebras,
Perplexity, Mistral, Cohere, Azure OpenAI, Vertex AI, Amazon Bedrock,
ElevenLabs, fal, Replicate, Luma, Deepgram, LMNT, Hume, AssemblyAI, Gladia,
and Rev.ai — with the same concepts and naming as the TypeScript original,
expressed in native Go (`context.Context`, `iter.Seq`, generics, typed
errors) rather than mirrored line-for-line.

**Status: v0.1.** The public API has full parity with the AI SDK 5 core;
AI SDK 6 parity is in progress (see the migration guide's
[AI SDK 6 delta](docs/migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta)). It's
implemented and tested end-to-end (unit tests plus a shared
provider-conformance suite), but it is young: expect rough edges, and
expect the API to move before a 1.0. Coming from the TypeScript SDK? Start
with [Migrating from the Vercel AI SDK](docs/migrating-from-vercel-ai-sdk.md).

## Install

```sh
go get github.com/azrtydxb/go-ai-sdk
```

Requires Go 1.26+.

## Quickstart

```go
package main

import (
	"context"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	model := anthropic.New().Model("claude-sonnet-5") // reads ANTHROPIC_API_KEY

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Why is the sky blue? Answer in one sentence.",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Text)
}
```

Streaming looks the same shape, with the result's parts consumed as a Go
iterator instead of a collected string:

```go
import "github.com/azrtydxb/go-ai-sdk/provider"

stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Count from one to five.",
})
if err != nil {
	panic(err)
}
defer stream.Close()

for part := range stream.Parts() {
	if delta, ok := part.(provider.TextDelta); ok {
		fmt.Print(delta.Text)
	}
}
if err := stream.Err(); err != nil {
	panic(err)
}
```

That's the whole surface for the two most common calls — everything else
(tool calling, structured output, embeddings, media, streaming internals,
middleware, MCP, telemetry, provider-specific options) is one guide away
in [`docs/`](docs/), starting at [Getting started](docs/getting-started.md).
Complete, runnable, env-guarded examples covering text, streaming, tools,
structured output, embeddings, images, speech, transcription, and MCP —
including the multi-step tool-calling loop and `ai.GenerateObject[T]` — live
in [`examples/`](examples/), each compiled by CI.

## Features

- **Text generation** — `ai.GenerateText`/`ai.StreamText`, with an
  automatic multi-step tool-calling loop (`StopWhen`, `PrepareStep`,
  `OnStepFinish`) and conversation continuation. Lifecycle callbacks
  (`OnModelCallStart`/`OnModelCallEnd`, `OnToolExecutionStart`/
  `OnToolExecutionEnd`) bracket each underlying model request and tool
  execution. See [Generating text](docs/core/generating-text.md).
- **Tool calling** — typed tools via `ai.NewTool[Args]` with a
  reflection-derived JSON Schema, `ActiveTools`, `RepairToolCall`, and a
  typed error taxonomy. See [Tools](docs/core/tools.md).
- **Tool-execution approvals** — `ai.RequireApproval`/`ai.ApprovalRequirer`
  gate a tool call on an inline decision (`ApproveToolCall`) or a
  suspend-then-resume flow (`PendingApprovals`/`Approvals`), with denials
  surfaced as a typed `*ai.ToolApprovalDeniedError` tool result. See
  [Tools § Approvals for tool execution](docs/core/tools.md#approvals-for-tool-execution).
- **RuntimeContext** — an arbitrary application-value bag
  (`GenerateTextOpts.RuntimeContext`, `ai.RuntimeContextFrom(ctx)`) threaded
  into every tool call, approval check, and inline approval decision for a
  run. See [Tools § RuntimeContext](docs/core/tools.md#runtimecontext).
- **Agents** — `agent.Agent` bundles a model, instructions, tools, and loop
  options for repeated runs (`Generate`/`Stream`), and `agent.AsTool`
  exposes one agent as a tool for another to delegate to. See
  [Agents](docs/core/agents.md).
- **Code Mode** — `codemode.Tool` wraps a set of tools into a single
  `run_code` tool the model writes short programs against, executed by a
  caller-supplied `Sandbox` (the SDK ships no runtime). See
  [Code Mode](docs/core/code-mode.md).
- **Structured output** — `ai.GenerateObject[T]`/`ai.StreamObject[T]`,
  native-JSON where a provider supports it and forced-tool-call mode
  otherwise. See [Structured output](docs/core/structured-output.md).
- **Output modes on `GenerateText`** — `GenerateTextOpts.Output`
  (`OutputObject[T]`, `OutputArray[T]`, `OutputChoice`, `OutputJSON`) decodes
  a `GenerateText` call's final text into a typed value, extracted via
  `ai.OutputAs[T]`, without a separate `GenerateObject` call
  (`GenerateText`-only for now; `StreamText` returns a typed error if
  `Output` is set). See
  [Generating text § Output modes](docs/core/generating-text.md#output-modes).
- **Streaming** — a `StreamPart` sequence (`iter.Seq`) covering text, tool
  calls, reasoning, and sources uniformly, plus `ai.SmoothStream` for
  steady-cadence UI rendering. See [Streaming](docs/core/streaming.md).
- **Reasoning/thinking** — surfaced uniformly as `ReasoningPart`/
  `ReasoningDelta`/`ReasoningEnd` across every provider that supports it,
  and requestable uniformly too: `GenerateTextOpts.Reasoning` maps a single
  `Effort`/`BudgetTokens` option onto each provider's native reasoning knob
  (`reasoning_effort`, `thinking`, `thinkingConfig`, or
  `additionalModelRequestFields.thinking`). See
  [Reasoning](docs/core/reasoning.md).
- **Embeddings** — `ai.Embed`/`ai.EmbedMany` with automatic batching and
  `ai.CosineSimilarity`. See [Embeddings](docs/core/embeddings.md).
- **Reranking** — `ai.Rerank` ranks documents by relevance to a query via
  `provider.RerankingModel` (Cohere this wave). See
  [Embeddings § Reranking](docs/core/embeddings.md#reranking).
- **Media** — image generation, speech synthesis, and transcription
  behind the same provider-agnostic pattern. See [Media](docs/core/media.md).
- **Middleware and registry** — compose behavior onto any
  `provider.LanguageModel` (`ExtractReasoningMiddleware`,
  `SimulateStreamingMiddleware`, `DefaultSettingsMiddleware`,
  `TelemetryMiddleware`) and resolve `"provider:model"` strings via
  `ai.Registry`. See [Middleware and registry](docs/core/middleware-and-registry.md).
- **Provider options** — a raw-wire-key escape hatch
  (`ProviderOptions`/`ProviderMetadata`) for provider-specific request
  parameters that don't have a dedicated field. See
  [Provider options](docs/core/provider-options.md).
- **Errors and retries** — every model call goes through a shared retry
  wrapper with typed, `errors.As`-able failure modes. See
  [Errors and retries](docs/core/errors-and-retries.md).
- **Telemetry** — a minimal, dependency-free span-reporting seam
  (`ai.Telemetry`) you bridge to OpenTelemetry or anything else — no OTel
  dependency shipped. See [Telemetry](docs/core/telemetry.md).
- **MCP (Model Context Protocol)** — a tools-only MCP client (stdio and
  Streamable HTTP transports) that adapts a server's tools straight into
  `ai.Tool`. See [MCP](docs/mcp.md).

## Documentation

- [Getting started](docs/getting-started.md) — install, first call, env
  vars, streaming quickstart
- **Core guides**: [Generating text](docs/core/generating-text.md) ·
  [Tools](docs/core/tools.md) ·
  [Structured output](docs/core/structured-output.md) ·
  [Agents](docs/core/agents.md) · [Code Mode](docs/core/code-mode.md) ·
  [Embeddings](docs/core/embeddings.md) · [Media](docs/core/media.md) ·
  [Streaming](docs/core/streaming.md) · [Reasoning](docs/core/reasoning.md) ·
  [Middleware and registry](docs/core/middleware-and-registry.md) ·
  [Provider options](docs/core/provider-options.md) ·
  [Errors and retries](docs/core/errors-and-retries.md) ·
  [Telemetry](docs/core/telemetry.md) · [MCP](docs/mcp.md)
- **Providers**: [overview and capability matrix](docs/providers/README.md),
  plus one page per vendor under [`docs/providers/`](docs/providers/)
- **Reference**: [Troubleshooting](docs/troubleshooting.md) ·
  [Migrating from the Vercel AI SDK](docs/migrating-from-vercel-ai-sdk.md) ·
  [Architecture](docs/architecture.md)
- [`docs/README.md`](docs/README.md) is the full index of the tree above.

## Provider and capability matrix

<!-- Summarizes docs/providers/README.md's canonical capability matrix. Update all three together (README.md, docs/providers/README.md, docs/core/media.md). -->

All supported providers, by capability:

| Capability | OpenAI | Anthropic | Google | Groq | xAI | DeepSeek³ | Together | Fireworks | Cerebras | Perplexity¹ | Mistral² | Cohere | Azure | Vertex AI | Bedrock |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `GenerateText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `StreamText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Tool calling | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | ✅ |
| `GenerateObject` / `StreamObject` | ✅ native | ✅ tool-mode | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ tool-mode⁴ |
| `Embed` / `EmbedMany` | ✅ | — | ✅ | — | — | — | ✅ | ✅ | — | — | ✅ | ✅ | ✅ | ✅ | ✅ |
| `Rerank`⁵ | — | — | — | — | — | — | — | — | — | — | — | ✅ | — | — | — |

**Structured output notes:**
- "Native" means the provider supports schema-constrained JSON output directly via native JSON mode.
- "Tool-mode" (Anthropic, Bedrock) uses an automatically injected, forced tool call — the same `GenerateObject[T]` call works identically either way.
- ¹ Perplexity: no tool-calling support in the live API; `Tools` in a `Call` are serialized but may be rejected or ignored.
- ² Mistral: `GenerateObject` uses `json_object` mode only; schema is not sent on the wire but enforced by the core-side decode step.
- ³ DeepSeek: `GenerateObject` uses `json_object` mode only (DeepSeek rejects `json_schema`); schema is not sent on the wire but enforced by the core-side decode step.
- ⁴ Bedrock: the Converse API has no schema-constrained JSON response mode (`Capabilities().NativeJSON` is `false`); `GenerateObject` falls back to a forced tool call, same as Anthropic.
- ⁵ `Rerank`: Cohere-only this wave (`ai.Rerank`, `provider.RerankingModel`); Voyage and Mixedbread are planned alongside their providers in a later wave. See [Embeddings § Reranking](docs/core/embeddings.md#reranking).

Supported providers for media capabilities:

| Capability | OpenAI | Google | Vertex AI | xAI | ElevenLabs | Groq | fal | Replicate | Luma | Deepgram | LMNT | Hume | AssemblyAI | Gladia | Rev.ai |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `GenerateImage` | ✅ gpt-image-1 | ✅ imagen-3.0-generate-002 | ✅ imagen-3.0-generate-002 | ✅ grok-2-image | — | — | ✅ fal-ai/flux/schnell | ✅ black-forest-labs/flux-schnell | ✅ photon-1 | — | — | — | — | — | — |
| `GenerateSpeech` | ✅ gpt-4o-mini-tts | — | — | — | ✅ eleven_multilingual_v2 | — | — | — | — | — | ✅ blizzard | ✅ (model ID has no wire effect) | — | — | — |
| `Transcribe` | ✅ whisper-1 | — | — | — | ✅ scribe_v1 | ✅ whisper-large-v3-turbo | — | — | — | ✅ nova-3 | — | — | ✅ universal | ✅ (model ID unused) | ✅ (model ID unused) |

**Note:** This closes out the Vercel-supported transcription roster (fal,
Replicate, Luma, Deepgram, LMNT, Hume, AssemblyAI, Gladia, Rev.ai). A
handful of AI SDK 6 providers remain unimplemented — planned per the
[v6 parity roadmap](docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md);
see [Provider coverage](#provider-coverage) below.

### Provider coverage

| Providers | Notes |
|---|---|
| OpenAI, Anthropic, Google (Gemini) | Three distinct wire formats prove the abstraction |
| Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| Mistral, Cohere | Own APIs, full provider implementations |
| Azure OpenAI, Vertex AI, Amazon Bedrock | Platform auth: Azure (API-key preset over the OpenAI-compatible base), Vertex AI (Google service-account/ADC auth), Bedrock (AWS SigV4 request signing) |
| ElevenLabs, fal, Replicate, Luma, Deepgram, LMNT, Hume, AssemblyAI, Gladia, Rev.ai; image/speech/transcription for OpenAI, Google/Vertex, xAI, Groq | Media-only or media-layered providers, all behind the same `ImageModel`/`SpeechModel`/`TranscriptionModel` interfaces |
| Planned, not yet implemented: Moonshot, Qwen, MiniMax, DeepInfra, Hugging Face, Baseten, LM Studio, NVIDIA NIM, Voyage, Mixedbread, Cartesia, Prodia, Black Forest Labs, AI Gateway | See the [v6 parity roadmap](docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md) (waves 13+) — the provider interface already accommodates them as follow-ups |

See [`CHANGELOG.md`](CHANGELOG.md) for the full release history, and the
[design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md) for
architecture, package layout, and the full decisions log.

## Contributing

See [`docs/architecture.md`](docs/architecture.md) for how the SDK is laid
out — the three-package-layer split, the OpenAI/Gemini "compat base"
pattern most providers build on, the `StreamResponse` disciplines every
streaming implementation follows, and step-by-step checklists for adding a
new provider or a new capability.

## License

[Apache License 2.0](LICENSE).
