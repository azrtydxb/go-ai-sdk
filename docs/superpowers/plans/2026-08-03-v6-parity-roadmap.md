# AI SDK 6 Parity Roadmap (Phase 2)

Target: full feature parity with Vercel AI SDK 6 (ai-sdk.dev, snapshot 2026-08-03), excluding the permanently-out-of-scope UI layer (useChat/useCompletion/useObject/RSC/UI transports/MCP Apps rendering) per the approved design spec.

Standing scope rulings:
- **WebRTC realtime**: skipped; WebSocket API variants implemented instead via a stdlib RFC-6455 client (`internal/websocket`).
- **OTel**: native bridge ships as a nested Go module (`contrib/otel/` with its own go.mod) so the root module stays zero-dependency.
- **DevTools / Terminal UI**: out-of-scope tooling; documented as such.
- **Code Mode / sandboxes**: shipped as a `Sandbox` interface the user implements; the SDK provides prompting, orchestration, and a documented contract — not a code runtime.
- All new providers fixture-tested only; live-testing status doc maintained.

## Waves

- **Wave 9 — v5 leftovers + quick v6 wins**: docs re-scope (v5-core→v6 target); call settings (TopK/PresencePenalty/FrequencyPenalty/Seed/Headers) wired through all providers; stop-condition helpers (HasToolCall, LoopFinished); OnAbort; multi-modal tool results; ExtractJSONMiddleware; image-model middleware (WrapImageModel); AssemblyAI + Gladia + Rev.ai transcription providers.
- **Wave 10 — structured output v6 + rerank + unified reasoning**: Output modes on GenerateText (text/object/array/choice/json + OutputAs[T]); provider.RerankingModel + ai.Rerank + Cohere/Voyage/Mixedbread rerank; unified Reasoning option (effort/budget) with per-provider mapping + precedence over ProviderOptions; full lifecycle-callback event set (OnLanguageModelCallStart/End, OnToolExecutionStart/End, embed/rerank events).
- **Wave 11 — agents**: agent package (ToolLoopAgent equivalent: model+instructions+tools+loop config, Generate/Stream; agent-as-tool subagents); tool-execution approval (approval func + policy hook + typed errors + resumable pending-approval flow); RuntimeContext/ToolsContext; Sandbox interface + context plumbing; Code Mode (sandbox-driven). Carried minors from wave 10's final review: consolidate the triplicated resolveBudgetTokens helper into provider; add SchemaDescription to GenerateTextOpts (injected output ToolDef currently has empty Description); note PrepareStep model-swap doesn't re-evaluate the Output NativeJSON capability check (doc sentence on PrepareStep); tool-mode Output with multiple output-tool calls in one response only answers the first.
- **Wave 12 — modalities**: provider.VideoModel + ai.GenerateVideo (Luma/Fal/Replicate video); internal/websocket; StreamTranscribe (Deepgram live, OpenAI realtime transcription); StreamTranslate; minimal Realtime voice session (OpenAI WS); UploadFile provider file references (OpenAI + Anthropic files APIs) usable in prompts; uploadSkill (Anthropic/OpenAI skills).
- **Wave 13 — MCP extensions + provider fleet**: Carried minors from wave 12's final review: consolidate the byte-identical fetchVideo helpers (luma/fal/replicate) into an internal/fetchmedia package; consider extracting the shared WS dial/readLoop/teardown machinery duplicated between openai realtime_transcription and RealtimeSession if a further WS consumer lands; websockettest's ReadMessage allocates unvalidated declared lengths (test-only, low priority). Original scope: MCP resources/templates/prompts/completions/elicitation/token-provider auth/drift detection/retries; new providers — Moonshot, Qwen (Alibaba), MiniMax, DeepInfra, Hugging Face, Baseten, LM Studio, NVIDIA NIM (openai-compat presets); Voyage (embed+rerank), Mixedbread (rerank), Cartesia (speech), Prodia + Black Forest Labs (image); AI Gateway provider.
- **Wave 14 — observability + docs**: contrib/otel nested-module bridge (GenAI semconv spans); telemetry metadata/custom tracer parity; docs corpus updated for everything (new pages, provider pages, matrices), migration guide re-baselined to v6, CHANGELOG v0.2.0, final whole-program gap re-audit vs live docs.

Each wave: full plan doc → SDD execution → final fable review → merge → push. No deferrals.
