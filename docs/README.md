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
- [Tools](core/tools.md) — `NewTool`, schema derivation, error taxonomy, `RepairToolCall`
- [Structured output](core/structured-output.md) — `GenerateObject` / `StreamObject`, native-JSON vs tool-mode
- [Embeddings](core/embeddings.md) — `Embed` / `EmbedMany`, batching, similarity
- [Media](core/media.md) — image, speech, and transcription generation
- [Streaming](core/streaming.md) — `StreamPart` reference, `SmoothStream`, iterator semantics
- [Reasoning](core/reasoning.md) — thinking/reasoning content across providers
- [Middleware and registry](core/middleware-and-registry.md) — `WrapModel`, built-in middlewares, `Registry`
- [Provider options](core/provider-options.md) — the raw-wire-key escape hatch, `ProviderMetadata`
- [Errors and retries](core/errors-and-retries.md) — the typed error reference, retry/backoff behavior
- [Telemetry](core/telemetry.md) — `Telemetry`, `TelemetryMiddleware`, OTel bridging
- [Model Context Protocol (MCP)](mcp.md) — using MCP servers' tools as `ai.Tool`s

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
- [Mistral](providers/mistral.md)
- [Cohere](providers/cohere.md)
- [ElevenLabs](providers/elevenlabs.md)

## Reference

- [Troubleshooting](troubleshooting.md) — auth failures, structured-output errors, streaming and tool-calling issues, MCP debugging
- [Migrating from the Vercel AI SDK](migrating-from-vercel-ai-sdk.md)
- [Architecture](architecture.md)
- [Changelog](../CHANGELOG.md) — release history

## Source of truth

This index mirrors the package layout under [`ai/`](../ai), [`provider/`](../provider),
and [`providers/`](../providers).
