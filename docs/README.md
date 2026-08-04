# go-ai-sdk documentation

`go-ai-sdk` is a Go SDK for building applications on top of large language
models, with a single, provider-agnostic API for text generation, structured
output, tool calling, streaming, embeddings, and media generation.

This is the index for the full documentation tree. Every page listed below
exists — the SDK's public API is implemented and documented end-to-end (see
[`CHANGELOG.md`](../CHANGELOG.md) for the release record).

## Start here

- [Getting started](getting-started.md) — install, first call, env vars, streaming quickstart

## Core guides

- [Generating text](core/generating-text.md) — `GenerateText`, the multi-step tool loop, conversation continuation
- [Tools](core/tools.md) — `NewTool`, schema derivation, error taxonomy, `RepairToolCall`, approvals, `RuntimeContext`
- [Structured output](core/structured-output.md) — `GenerateObject` / `StreamObject`, native-JSON vs tool-mode
- [Agents](core/agents.md) — `agent.Agent`, `RunOpts`, `AsTool` sub-agent delegation
- [Code Mode](core/code-mode.md) — `codemode.Tool`, the `Sandbox` contract, `APIDoc`
- [Embeddings](core/embeddings.md) — `Embed` / `EmbedMany`, batching, similarity
- [Media](core/media.md) — image, video, speech, transcription (including
  live streaming), and translation generation
- [Streaming](core/streaming.md) — `StreamPart` reference, `SmoothStream`, iterator semantics, suspension in streams
- [Reasoning](core/reasoning.md) — thinking/reasoning content across providers
- [Middleware and registry](core/middleware-and-registry.md) — `WrapModel`, built-in middlewares, `Registry`
- [Provider options](core/provider-options.md) — the raw-wire-key escape hatch, `ProviderMetadata`
- [Errors and retries](core/errors-and-retries.md) — the typed error reference, retry/backoff behavior
- [Telemetry](core/telemetry.md) — `Telemetry`, `TelemetryMiddleware`, and
  the real [`contrib/otel`](../contrib/otel/README.md) OpenTelemetry
  bridge (a separate Go module)
- [Model Context Protocol (MCP)](mcp.md) — tools, resources, prompts,
  completions, elicitation, and token-provider auth/retries

## Providers

- [Provider overview](providers/README.md) — capability matrix and links to every provider page
- [OpenAI](providers/openai.md)
- [Anthropic](providers/anthropic.md)
- [Google](providers/google.md)
- [Vertex AI](providers/vertex.md)
- [Azure OpenAI](providers/azure.md)
- [Amazon Bedrock](providers/bedrock.md)
- [Groq](providers/groq.md)
- [xAI](providers/xai.md)
- [DeepSeek](providers/deepseek.md)
- [Cerebras](providers/cerebras.md)
- [Together AI](providers/together.md)
- [Fireworks](providers/fireworks.md)
- [Perplexity](providers/perplexity.md)
- [Moonshot](providers/moonshot.md)
- [Qwen](providers/qwen.md)
- [MiniMax](providers/minimax.md)
- [DeepInfra](providers/deepinfra.md)
- [Hugging Face](providers/huggingface.md)
- [Baseten](providers/baseten.md)
- [LM Studio](providers/lmstudio.md)
- [NVIDIA NIM](providers/nvidia.md)
- [Vercel AI Gateway](providers/gateway.md)
- [Mistral](providers/mistral.md)
- [Cohere](providers/cohere.md)
- [Voyage](providers/voyage.md)
- [Mixedbread](providers/mixedbread.md)
- [ElevenLabs](providers/elevenlabs.md)
- [fal](providers/fal.md)
- [Replicate](providers/replicate.md)
- [Luma](providers/luma.md)
- [Deepgram](providers/deepgram.md)
- [LMNT](providers/lmnt.md)
- [Hume](providers/hume.md)
- [AssemblyAI](providers/assemblyai.md)
- [Gladia](providers/gladia.md)
- [Rev.ai](providers/revai.md)
- [Cartesia](providers/cartesia.md)
- [Prodia](providers/prodia.md)
- [Black Forest Labs](providers/bfl.md)

## Versioning

`go-ai-sdk` is pre-1.0: the public API described throughout this tree is
implemented and tested end-to-end, but may still change before `v1.0.0`.
Once the project reaches 1.0, it follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). For the current
release and every notable change — release or not —
see [`CHANGELOG.md`](../CHANGELOG.md) and the
[GitHub releases](https://github.com/azrtydxb/go-ai-sdk/releases). The only
breaking change to date landed in `v0.2.0`: `ai.Telemetry.OnSpanStart`
gained a leading `ctx` parameter (see
[Telemetry](core/telemetry.md#telemetry-and-spaninfo)).

`contrib/otel` (the OpenTelemetry bridge) is versioned as its own nested Go
module, tagged alongside the root (`contrib/otel/vX.Y.Z`) — see
[Architecture § Observability](architecture.md#observability-and-the-nested-contribotel-module).

## Reference

- [Troubleshooting](troubleshooting.md) — auth failures, structured-output errors, streaming and tool-calling issues, MCP debugging
- [Migrating from the Vercel AI SDK](migrating-from-vercel-ai-sdk.md)
- [Architecture](architecture.md)
- [Changelog](../CHANGELOG.md) — release history

## Source of truth

This index mirrors the package layout under [`ai/`](../ai), [`provider/`](../provider),
and [`providers/`](../providers).
