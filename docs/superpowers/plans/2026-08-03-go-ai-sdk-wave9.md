# go-ai-sdk Wave 9 Implementation Plan (v5 leftovers + v6 quick wins)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining v5-core gaps and land the cheap AI SDK 6 wins: first-class call settings (TopK/penalties/Seed/Headers), stop-condition helpers, OnAbort, multi-modal tool results, two new middlewares, and the three missing transcription providers (AssemblyAI, Gladia, Rev.ai). Re-scope the docs' parity claim.

**Architecture:** Additive fields on `provider.Call` + `ai.GenerateTextOpts` threaded through every provider's request builder; new helpers/middlewares in `ai`; three new standalone transcription providers (Gladia and Rev.ai are async — reuse the Luma-style poll pattern; AssemblyAI is async too: upload→transcript→poll).

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies; ADDITIVE only on existing surfaces.
- Providers never retry; non-2xx → `ai.NewAPICallError`; ctx passthrough; ProviderOptions conventions unchanged (new first-class settings take precedence rules: ProviderOptions still win, since they merge last — document).
- Existing tests green; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean per commit; providertest may gain NOTHING this wave.
- New providers carry the live-API doc note; live-testing status doc updated.
- Commits conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: First-class call settings — TopK, PresencePenalty, FrequencyPenalty, Seed, Headers

**Files:**
- Modify: `provider/call.go` (+`TopK *int`, `PresencePenalty *float64`, `FrequencyPenalty *float64`, `Seed *int64`, `Headers map[string]string`), `ai/options.go` (same fields on GenerateTextOpts; threaded in buildCall), `ai/generate_object.go` (Seed? — no: keep GenerateObjectOpts unchanged this wave)
- Modify request builders: `internal/openaicompat/wire.go` (`top_k`? OpenAI has no top_k — omit; `presence_penalty`, `frequency_penalty`, `seed`), `internal/geminicompat/wire.go` (generationConfig `topK`, `presencePenalty`, `frequencyPenalty`, `seed`), `providers/anthropic/wire.go` (`top_k`; penalties/seed unsupported → ignored with comment), `providers/cohere/wire.go` (`k` for top_k? Cohere v2 uses `k`: verify — use `k`; `presence_penalty`, `frequency_penalty`, `seed`), `providers/mistral/wire.go` (`random_seed` for Seed; `presence_penalty`, `frequency_penalty`; top_k unsupported → comment), `providers/bedrock/wire.go` (inferenceConfig has no topK/penalties/seed for Converse base — route TopK to `additionalModelRequestFields.top_k`? NO: unsupported → ignored with comment; keep it honest)
- `Headers`: per-call extra HTTP headers, applied by every provider's request path AFTER auth headers (caller-set headers win except the auth header itself — document); implement in openaicompat/geminicompat/anthropic/cohere/mistral/bedrock (bedrock: headers participate in SigV4 signing ONLY if x-amz-*; plain extra headers are unsigned — document) + media providers' language/embedding paths where trivially shared (language-model paths are required; media paths optional this wave — document which).
- Tests: per-builder request-shape tests for each supported field; unsupported-field ignore comments verified by absence tests where cheap; Headers test per family (header present; auth not clobbered).

**Precedence note (document on the fields):** first-class fields serialize into the SDK-built body; ProviderOptions merge afterwards and still win on key collision.

- [ ] **Step 1: Failing tests per family → implement → pass. Full check suite. Commit** — `feat: first-class TopK, penalties, Seed, and per-call Headers`

---

### Task 2: Stop helpers, OnAbort, multi-modal tool results, two middlewares

**Files:**
- Modify: `ai/options.go` (+`OnAbort func()` — fires when StreamText iteration is abandoned or ctx canceled mid-stream, once, before Close; document exact semantics), `ai/stream_text.go`
- Create: helpers in `ai/options.go`: `HasToolCall(names ...string) func([]Step) bool` (stops when the last step called any named tool; empty names = any tool call) and `LoopFinished() func([]Step) bool` (stops when the last step made NO tool calls — parity with isLoopFinished; note StopWhen is only consulted on tool-call steps today, so LoopFinished needs the loop to consult StopWhen on EVERY step — change the consultation rule: StopWhen now runs after every step; document; existing StepCountIs semantics unaffected)
- Multi-modal tool results: `ai.ToolResultContent{Text string; Images []provider.GeneratedImage}` — when a Tool's Execute returns a `ToolResultContent` (or `*ToolResultContent`), providers that support image tool results (anthropic: tool_result content blocks with image; geminicompat: functionResponse parts limitation — text-only, document; openaicompat: text-only, document) serialize the images; others stringify the text portion. Wire: anthropic tool_result content array [{type:text},{type:image,source:...}]. Tests: anthropic wire shape; fallback stringification elsewhere.
- Create: `ai/middleware_json.go`: `ExtractJSONMiddleware(model)` — strips markdown fences from text content (Generate + Stream: buffer-and-strip fence lines incrementally; reuse the fence-stripping logic from generate_object, extract shared helper `internal/fences` or ai-internal func). `WrapImageModel(m provider.ImageModel, wrap func(provider.ImageModel) provider.ImageModel) provider.ImageModel` naming hook (parity with WrapModel).
- Tests: HasToolCall/LoopFinished in both loops; StopWhen-every-step rule regression (existing StopWhen tests still pass — they used tool-call steps); OnAbort fires once on abandon + on ctx cancel, not on natural end; ExtractJSON both modes; WrapImageModel smoke.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: stop helpers, OnAbort, multi-modal tool results, ExtractJSON middleware`

---

### Task 3: AssemblyAI transcription provider

**Files:**
- Create: `providers/assemblyai/{assemblyai.go,transcription.go}` (+tests)

Wire (async): `New` (WithAPIKey env `ASSEMBLYAI_API_KEY`, WithBaseURL default `https://api.assemblyai.com`, WithHTTPClient, WithPollInterval default 500ms). `TranscriptionModel(id)` (model id → `speech_model` field, e.g. "universal"; empty id → field omitted). Flow: (1) `POST /v2/upload` with raw audio bytes, header `authorization: <key>` → `{"upload_url":...}`; (2) `POST /v2/transcript` `{"audio_url":..., "speech_model":..., "language_code":Language(omit "")}` + ProviderOptions["assemblyai"] top-level merge → `{"id","status"}`; (3) poll `GET /v2/transcript/{id}` until status "completed" (→ `{"text","words":[{"text","start","end"}],"language_code","audio_duration"}` — start/end are MILLISECONDS → divide by 1000) or "error" (→ error incl. `"error"` field). Segments from words; DurationSec from audio_duration (seconds). Prompt unsupported → ignored w/ comment.
Tests: 3-endpoint fixture, request shapes, ms→sec conversion, error status, options merge, 401 → APICallError, ctx cancel mid-poll.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: AssemblyAI transcription provider`

---

### Task 4: Gladia + Rev.ai transcription providers

**Files:**
- Create: `providers/gladia/{gladia.go,transcription.go}`, `providers/revai/{revai.go,transcription.go}` (+tests)

**Gladia** (async): env `GLADIA_API_KEY`, base `https://api.gladia.io`, header `x-gladia-key`. Flow: (1) `POST /v2/upload` multipart field `audio` (filename per MediaType ext) → `{"audio_url":...}`; (2) `POST /v2/pre-recorded` `{"audio_url":...}` (+`{"language":Language}` when set... Gladia uses `language` inside `{"language_config":{"languages":[..]}}` — SIMPLIFY per docs: send `{"audio_url", "custom_metadata"?}` + ProviderOptions top-level; Language when set → `"language_config":{"languages":[Language]}`) → `{"id","result_url"}`; (3) poll `GET /v2/pre-recorded/{id}` until `status` "done" (→ `result.transcription.full_transcript`, `result.transcription.utterances[]{text,start,end}` seconds → segments; `result.metadata.audio_duration`) or "error". PollInterval option.
**Rev.ai** (async): env `REVAI_API_KEY` (also accept `REV_AI_API_KEY` fallback), base `https://api.rev.ai`, `Authorization: Bearer`. Flow: (1) `POST /speechtotext/v1/jobs` multipart `media` file + `options` JSON part `{"language":Language(omit)}` + ProviderOptions merged into the options JSON → `{"id","status"}`; (2) poll `GET /speechtotext/v1/jobs/{id}` until "transcribed" (or "failed" → error w/ `failure_detail`); (3) `GET /speechtotext/v1/jobs/{id}/transcript` with `Accept: application/vnd.rev.transcript.v1.0+json` → `{"monologues":[{"elements":[{"type":"text","value","ts","end_ts"}...]}]}` — text = concatenation of element values (type text + punct); segments = per element of type "text" {value, ts, end_ts}; duration = last end_ts.
Tests per provider: full fixture flows, request shapes, options merge, error status, 401, ctx cancel.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: Gladia and Rev.ai transcription providers`

---

### Task 5: Docs re-scope + wave-9 docs

**Files:**
- Modify: `README.md` (parity claim → "full parity with the AI SDK 5 core; AI SDK 6 parity in progress — tracked in the migration guide"; transcription matrix +3; not-yet list updated), `docs/migrating-from-vercel-ai-sdk.md` (re-baseline: add an "AI SDK 6 delta" section listing the v6 features and their status: shipped in this wave / planned wave 10-14 per the roadmap / out of scope, referencing docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md), `docs/core/generating-text.md` (new settings + helpers + OnAbort), `docs/core/tools.md` (multi-modal results), `docs/core/middleware-and-registry.md` (ExtractJSON, WrapImageModel), `docs/getting-started.md` (env rows +3), `docs/providers/README.md` (+3 pages, matrices, live-testing note), NEW `docs/providers/{assemblyai,gladia,revai}.md`, `docs/core/media.md` matrices, `CHANGELOG.md` (Unreleased entries), `docs/troubleshooting.md` (auth bullets).
- Same verification discipline as wave 7 (snippets compile-verified, claims grepped, cell counts, links).

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 9 — settings, helpers, transcription providers, v6 re-scope`

---

## Self-Review Notes

- v6-delta ledger for THIS wave: call settings ✓ (headers/timeout partial: Headers yes; Vercel's rich `timeout` object deferred to wave 10's lifecycle work — noted in migration doc), hasToolCall/isLoopFinished ✓, onAbort ✓, multi-modal tool results ✓ (anthropic-first, others documented), extractJsonMiddleware ✓, wrapImageModel ✓ (naming hook; full image middleware interface deferred — noted), AssemblyAI/Gladia/Rev.ai ✓.
- StopWhen consultation-rule change (every step, not just tool-call steps) is the one behavior change — audit existing tests in Task 2 and document in the field's comment + migration divergence list (it REMOVES divergence: Vercel consults on every step).
- Async transcription providers reuse the Luma poll discipline (ctx-aware sleep, WithPollInterval, terminal-state handling).
