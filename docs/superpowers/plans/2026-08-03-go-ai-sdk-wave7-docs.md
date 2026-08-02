# go-ai-sdk Wave 7 (Documentation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extensive, accurate documentation for the feature-complete SDK: a docs/ tree covering every feature, a page per provider, a Vercel-migration guide, contributor architecture notes, godoc coverage for every package, and an overhauled README.

**Architecture:** Markdown under `docs/` (guide pages + per-provider pages), discoverable from a docs index and the README. Every code snippet MUST be verified against the real API — the writer compiles each snippet (scratch `go build` of a temp main wrapping it) before committing; reviewers fact-check signatures against source. No generated-site tooling (plain GitHub-renderable markdown).

**Tech Stack:** Markdown; Go 1.26 for snippet verification.

## Global Constraints

- Every code snippet compile-verified against the current API before commit; every capability claim verified against code (grep the constructor/method). Wrong docs are Critical findings.
- Tables: header/delimiter cell counts must match everywhere.
- Relative links between docs pages must resolve (check with a link pass).
- `go vet ./... && go build ./... && go test ./... && gofmt -l .` stays clean (doc.go additions compile).
- Commit messages conventional (`docs:`), each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Docs skeleton + getting started + text generation + tools + structured output

**Files:**
- Create: `docs/README.md` (index with the full planned tree), `docs/getting-started.md`, `docs/core/generating-text.md`, `docs/core/tools.md`, `docs/core/structured-output.md`

**Content requirements:**
- getting-started: install (`go get github.com/azrtydxb/go-ai-sdk@latest`), module requirements (Go 1.26+, zero deps), first GenerateText call (openai + anthropic variants), env-var table for ALL 16 providers (grep each provider's New() for the env name), streaming quickstart, where to go next.
- generating-text: GenerateText opts field-by-field (System/Prompt/Messages exclusivity, MaxTokens/Temperature/TopP/StopSequences/MaxRetries), multi-step tool loop (MaxSteps, StopWhen + StepCountIs, default-cap-16 rule), PrepareStep (StepPlan incl. model swap + persistence rule), OnStepFinish (+ StreamText abandonment caveat), OnFinish/OnError (+ validation-error exclusion rule), Steps/Usage/Messages result anatomy, conversation continuation (Messages round-trip), retries + RetryError.
- tools: NewTool[Args] with jsonschema tags (description=, enum=, escaping), schema derivation rules (required/omitempty/pointers, embedded structs), execution error taxonomy (InvalidToolArguments/ToolExecution/NoSuchTool), IsError result convention, ActiveTools, RepairToolCall (single-shot rule), MCP tools cross-link.
- structured-output: GenerateObject/StreamObject with a full worked example (struct → schema → result), native-JSON vs tool-mode per provider (Capabilities), mistral json_object + deepseek JSONObjectOnly footnotes, NoObjectGeneratedError handling, StreamObject partials semantics + Final()/Close().
- Every page: "Source of truth" footer linking the relevant source files.

- [ ] **Step 1: Write pages; compile-verify every snippet. Step 2: full check suite. Commit** — `docs: getting started, text generation, tools, structured output`

---

### Task 2: Embeddings, media, streaming, reasoning, middleware/registry, provider options, errors, telemetry, MCP

**Files:**
- Create: `docs/core/embeddings.md`, `docs/core/media.md`, `docs/core/streaming.md`, `docs/core/reasoning.md`, `docs/core/middleware-and-registry.md`, `docs/core/provider-options.md`, `docs/core/errors-and-retries.md`, `docs/core/telemetry.md`, `docs/mcp.md`

**Content requirements:**
- embeddings: Embed/EmbedMany, batching + MaxBatchSize table (per provider — verify each), EmbeddingModelWithOptions, CosineSimilarity.
- media: GenerateImage/GenerateSpeech/Transcribe with worked examples, per-provider matrices (image: openai/google/vertex/xai; speech: openai/elevenlabs; transcription: openai/groq/elevenlabs), Size-vs-AspectRatio rules, format/voice defaults, FilePart attachment matrix (anthropic pdf / gemini any / openai pdf / bedrock documents w/ format table).
- streaming: StreamPart type reference (every variant incl. ReasoningDelta/End, SourceEvent, FinishPart w/ ProviderMetadata), single-use iterator + Err() pattern, Close(), accessors, SmoothStream (chunking constants, no-default-delay divergence), OnChunk ordering vs SmoothStream.
- reasoning: enabling anthropic thinking via ProviderOptions (worked example w/ budget_tokens), deepseek reasoning_content, bedrock reasoningContent, redacted handling (excluded from ReasoningText, preserved in Content), signature round-trip + unsigned-part skip rule, ExtractReasoningMiddleware (both modes, incremental guarantees), usage detail fields.
- middleware-and-registry: WrapModel, the three middlewares (opts structs), composition order guidance, Registry (Register + five lookups, "provider:model" split rule, colon-safe).
- provider-options: the namespaced raw-wire-key convention (Vercel divergence callout with contrast example), per-provider merge point (top-level JSON, multipart form fields), ProviderMetadata (both APIs, per-provider contents: anthropic cache_creation, openai-compat system_fingerprint).
- errors-and-retries: full typed-error reference with errors.As examples, retryability rules (429/408/5xx), backoff parameters, RetryError anatomy.
- telemetry: Telemetry/SpanInfo, TelemetryMiddleware, stream span lifecycle, OTel-bridge sketch (interface impl example — compile-verified), single-consumer caveat.
- mcp: full client walkthrough (stdio + streamable HTTP), transports' documented deviations/assumptions, Tools() adapter + tool-loop example, protocol version pinned 2025-03-26, limitations list (tools-only, text-content-only results).

- [ ] **Step 1: Write pages; compile-verify snippets. Step 2: full check suite. Commit** — `docs: core feature guides and MCP`

---

### Task 3: Provider pages (16) + provider index

**Files:**
- Create: `docs/providers/README.md` (full capability matrix + links), and one page each: `docs/providers/{openai,anthropic,google,vertex,azure,bedrock,groq,xai,deepseek,cerebras,together,fireworks,perplexity,mistral,cohere,elevenlabs}.md`

**Per-page template (verify EVERY line against the provider's source):** construction + options (env var, default base URL, auth mechanism); supported capabilities with model-constructor list; capability notes/quirks (e.g. anthropic tool-mode objects + thinking; deepseek json_object-only; perplexity no live tools; mistral schema-dropped; azure deployment names + api-key; vertex auth paths incl. gauth/ADC file + global location rule; bedrock SigV4 + region resolution + Converse limits + document formats; elevenlabs voice/format mapping); ProviderOptions examples with REAL wire keys for that provider; footnote parity with README tables.

- [ ] **Step 1: Write index + 16 pages; verify claims per source. Step 2: full check suite. Commit** — `docs: provider reference pages`

---

### Task 4: Migration guide, architecture, godoc pass, README overhaul, CHANGELOG

**Files:**
- Create: `docs/migrating-from-vercel-ai-sdk.md`, `docs/architecture.md`, `CHANGELOG.md`, `doc.go` files where packages lack a package doc comment (audit: ai, provider, providers/* [16], internal packages excluded from godoc need none but keep if present, mcp, provider/providertest)
- Modify: `README.md` (overhaul: hero example, feature bullets, docs-tree links, provider matrix stays, contributing pointer)

**Content requirements:**
- migration guide: side-by-side TS→Go API mapping table (generateText→GenerateText etc., every core function + option names), concept mapping (Zod→struct tags, providerOptions camelCase→wire keys, stopWhen, middleware names, streams: async iterators→iter.Seq), documented divergences consolidated in one list (raw wire keys, SmoothStream no default delay, telemetry interface not OTel, MCP tools-only, OnError validation exclusion, ActiveTools resolution semantics), features NOT ported (UI hooks/RSC — permanent; anthropic citations, provider-executed tools, OTel-native — future).
- architecture: the three layers, compat bases (openaicompat/geminicompat), conformance suite philosophy, StreamResponse disciplines (yield rules, truncation rule, single FinishPart), replay-safe informational parts rule, how to add a provider (checklist referencing providertest), how to add a capability.
- CHANGELOG: one entry per wave (Keep-a-Changelog style, Unreleased → v0.1.0 pending tag) summarizing waves 1–7 from git history.
- godoc: every non-internal package has a package comment (doc.go or existing file) describing purpose + a minimal example reference.

- [ ] **Step 1: Write everything; compile checks for doc.go. Step 2: full check suite. Commit** — `docs: migration guide, architecture, changelog, godoc pass`

---

### Task 5: Link/consistency pass + docs index finalization

**Files:**
- Modify: any docs file with issues found; `docs/README.md` finalized

**Work:** verify every relative link resolves (script a quick grep/check in-session); every table's cell counts; terminology consistency (provider names, "wave" mentions removed from user-facing docs — waves are internal history, docs speak in features); snippet spot-recompile (sample 10 across pages); README ↔ docs matrix consistency; spec doc gains a final "## Status: feature-complete (v0.1.0 candidate)" note.

- [ ] **Step 1: Run the passes, fix everything found. Step 2: full check suite. Commit** — `docs: consistency and link pass`

---

## Self-Review Notes

- Coverage: every shipped feature from waves 1–6 has a home page; all 16 providers documented; migration + architecture + changelog + godoc close the "extensive documentation" mandate.
- The compile-verification rule is the plan's core quality gate — reviewers must treat unverifiable/wrong snippets as Critical.
- Waves terminology confined to CHANGELOG/spec; user-facing docs are feature-oriented.
