# go-ai-sdk Wave 5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Core-parity features vs the Vercel AI SDK: ProviderOptions actually wired, reasoning support (thinking/reasoning tokens), middleware set, provider registry, agent-loop controls (StopWhen/PrepareStep/OnStepFinish), stream smoothing, sources, cosine similarity, and richer usage accounting.

**Architecture:** Additive, non-breaking extensions. ProviderOptions adopts the Vercel convention (`map[provider-name]map[string]any` shallow-merged into the request JSON). Reasoning becomes a first-class content/stream part. Middlewares are `provider.LanguageModel` decorators in `ai`. The registry maps `"provider:model"` ids onto the uniform `Model(id)`/`EmbeddingModel(id)`-style constructors all 16 providers already share.

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- All changes ADDITIVE — no breaking changes to existing exported API (new fields/functions/types only).
- Existing tests stay green after every task; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit.
- providertest must not be weakened; it MAY gain additive scenarios only if every existing provider passes unmodified.
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Wire ProviderOptions everywhere

**Files:**
- Modify: `internal/openaicompat/{wire.go,image.go,speech.go,transcription.go,embedding.go}`, `internal/geminicompat/{wire.go,image.go,embedding.go}`, `providers/anthropic/wire.go`, `providers/cohere/wire.go`, `providers/mistral/wire.go`, `providers/bedrock/wire.go`, `providers/elevenlabs/{speech.go,transcription.go}`, `providers/vertex/embedding.go`
- Modify: `ai/options.go`, `provider/call.go` (doc comments only — drop "reserved/ignored"), README status note
- Test: per-package request-shape tests

**Semantics (uniform rule, document on `provider.Call.ProviderOptions`):**
- `ProviderOptions` is keyed by provider name (`Call.ProviderOptions["anthropic"]`), value `map[string]any`. Each provider looks up ITS key (`cfg.Name` for compat bases — so azure reads `"azure"`, groq reads `"groq"`) and shallow-merges the entries into the top-level request JSON object AFTER building it (option entries win over SDK-built fields). Non-`map[string]any` values under the provider key are ignored. Other providers' keys are ignored.
- Implementation helper: one shared func per compat base / provider file: `applyProviderOptions(body map[string]any, opts map[string]any, name string)`. Since most request builders marshal structs (not maps), the merge point is: marshal struct → unmarshal into `map[string]any` → merge → marshal. To avoid that cost when no options are set (the common case), short-circuit: `if len(providerOptions[name]) == 0 { return structBytes }`.
- ImageCall/SpeechCall/TranscriptionCall ProviderOptions follow the same rule. `ai.GenerateTextOpts.ProviderOptions` already threads into Call — same for the media opts (verify; wire where missing).
- Bedrock: merge into the Converse body JSON (e.g. `additionalModelRequestFields` can be set wholesale by the caller). ElevenLabs speech: merge into the TTS JSON body (e.g. `voice_settings` override). ElevenLabs transcription + openaicompat transcription (multipart): merge as extra form fields — values stringified with `fmt.Sprint` (document).
- Tests per package: an option key overriding an SDK field (e.g. `"temperature": 0.9` beats opts) + a novel key passing through (e.g. anthropic `"top_k": 5`, openai `"logprobs": true`, google merge into top level).

- [ ] **Step 1: Failing request-shape tests per package → Step 2: implement (shared helper per package) → Step 3: pass. Step 4: docs de-reservation. Full check suite. Commit** — `feat: wire ProviderOptions through all providers`

---

### Task 2: Reasoning support + usage details

**Files:**
- Modify: `provider/message.go` (+`ReasoningPart`), `provider/stream.go` (+`ReasoningDelta`), `provider/response.go` (+`Response.ReasoningText()` helper; `Usage` gains `CachedInputTokens`, `ReasoningTokens int`)
- Modify: `providers/anthropic/{wire.go,language_model.go}` (thinking blocks), `internal/openaicompat/{wire.go,language_model.go}` (`reasoning_content`, usage detail fields), `ai/generate_text.go` (+`GenerateTextResult.ReasoningText`, `Step.ReasoningText`), `ai/stream_text.go` (+`TextStream.ReasoningText()`)
- Test: each touched package

**Semantics:**
- `provider.ReasoningPart{Text string; Redacted bool; Signature string}` (content part; Signature/Redacted for Anthropic round-tripping). `provider.ReasoningDelta{Text string}` (stream part).
- **Anthropic**: response `thinking` blocks → `ReasoningPart{Text: thinking, Signature: signature}`; `redacted_thinking` → `ReasoningPart{Redacted: true, Text: data}`. Assistant-message conversion sends reasoning parts back as `thinking`/`redacted_thinking` blocks (signature preserved) BEFORE other blocks (Anthropic requires thinking first). Streaming: `content_block_delta` `thinking_delta` → ReasoningDelta; `signature_delta` accumulates into the part emitted at `content_block_stop` (no new stream part for signatures — the accumulated ReasoningPart lands in the step content; simplest conforming behavior: emit ReasoningDelta for thinking_delta text and nothing for signature_delta). Enabling thinking is via ProviderOptions (Task 1): `{"anthropic": {"thinking": {"type":"enabled","budget_tokens":N}}}` — no new typed knob (document in the package doc).
- **openaicompat**: response `choices[0].message.reasoning_content` (DeepSeek-R1 style) → ReasoningPart; stream `delta.reasoning_content` → ReasoningDelta. Usage details: `usage.prompt_tokens_details.cached_tokens` → `Usage.CachedInputTokens`; `usage.completion_tokens_details.reasoning_tokens` → `Usage.ReasoningTokens`.
- **Anthropic usage**: `cache_read_input_tokens` → `CachedInputTokens`.
- **ai**: `Step.ReasoningText` (concatenated reasoning parts of that step's response), `GenerateTextResult.ReasoningText` (last step's), `TextStream.ReasoningText()` accumulated. Reasoning parts must NOT leak into `.Text()`.

- [ ] **Step 1: provider types + failing tests → Step 2: anthropic + openaicompat wiring → Step 3: ai plumbing → all pass. Full check suite. Commit** — `feat: reasoning parts, thinking support, usage details`

---

### Task 3: Middlewares + registry + CosineSimilarity

**Files:**
- Create: `ai/middleware.go` (three middlewares), `ai/registry.go`, `ai/similarity.go`
- Test: `ai/middleware_test.go`, `ai/registry_test.go`, `ai/similarity_test.go`

**Produces:**

```go
// ai/middleware.go — all return a wrapped provider.LanguageModel (WrapModel-compatible).
// ExtractReasoningMiddleware pulls <tag>...</tag> spans out of text content
// (Generate: from TextParts; Stream: stateful tag-scanning across TextDeltas)
// and re-emits them as ReasoningPart/ReasoningDelta. startWithReasoning
// handles models that omit the opening tag. Tag name without angle brackets,
// e.g. "think".
func ExtractReasoningMiddleware(model provider.LanguageModel, tagName string) provider.LanguageModel

// SimulateStreamingMiddleware makes Stream() call Generate() and replay the
// response as a synthetic stream: ReasoningDeltas first, then TextDelta(s),
// ToolCallEnd per tool call, one FinishPart.
func SimulateStreamingMiddleware(model provider.LanguageModel) provider.LanguageModel

// DefaultSettingsMiddleware fills zero-valued Call fields from defaults
// (Temperature/TopP/MaxTokens/StopSequences/ProviderOptions merged;
// per-call values win).
func DefaultSettingsMiddleware(model provider.LanguageModel, defaults provider.Call) provider.LanguageModel

// ai/registry.go
type LanguageModelProvider interface{ Model(id string) provider.LanguageModel }
type EmbeddingModelProvider interface{ EmbeddingModel(id string) provider.EmbeddingModel }
type ImageModelProvider interface{ ImageModel(id string) provider.ImageModel }
type SpeechModelProvider interface{ SpeechModel(id string) provider.SpeechModel }
type TranscriptionModelProvider interface{ TranscriptionModel(id string) provider.TranscriptionModel }

type Registry struct{ /* unexported maps */ }
func NewRegistry() *Registry
func (r *Registry) Register(name string, p any) // stores p; capability checked at lookup
// Lookup id "provider:model" (SplitN on first ':'); errors:
// "ai: invalid model id %q (want \"provider:model\")", "ai: unknown provider %q",
// "ai: provider %q does not support language models" (etc. per capability).
func (r *Registry) LanguageModel(id string) (provider.LanguageModel, error)
func (r *Registry) EmbeddingModel(id string) (provider.EmbeddingModel, error)
func (r *Registry) ImageModel(id string) (provider.ImageModel, error)
func (r *Registry) SpeechModel(id string) (provider.SpeechModel, error)
func (r *Registry) TranscriptionModel(id string) (provider.TranscriptionModel, error)

// ai/similarity.go
// CosineSimilarity returns the cosine similarity of two equal-length
// vectors; error on length mismatch or zero-magnitude vector.
func CosineSimilarity(a, b []float64) (float64, error)
```

Tests: extract-reasoning both modes (Generate + Stream, tag split across deltas, startWithReasoning); simulate-streaming replays a tool-call response correctly; default-settings per-call-wins semantics; registry happy paths for all five capabilities against real provider values (openai.New etc.) + all error strings; cosine known values + error cases. Model IDs containing ':' (bedrock "anthropic.claude-3:1") must round-trip — SplitN(2) keeps the rest intact; test it.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: middlewares, provider registry, cosine similarity`

---

### Task 4: Agent-loop controls — StopWhen, PrepareStep, OnStepFinish

**Files:**
- Modify: `ai/options.go`, `ai/generate_text.go`, `ai/stream_text.go`
- Test: `ai/tool_loop_test.go`, `ai/stream_text_test.go` additions

**Produces (fields on `GenerateTextOpts`, honored by BOTH GenerateText and StreamText):**

```go
// StopWhen, when set, decides after each completed step whether to stop the
// tool loop (return true = stop). Evaluated only when the step requested tool
// calls; MaxSteps still applies as a hard cap (default 1 unless StopWhen set,
// in which case default cap is 16 — document).
StopWhen func(steps []Step) bool
// PrepareStep, when set, is called before each model call with the step
// index and the planned Call; it may return a modified Call (e.g. swap
// tools, tighten ToolChoice). Returning the zero Call means "unchanged"
// — determined by a `bool` second return.
PrepareStep func(stepIndex int, call provider.Call) (provider.Call, bool)
// OnStepFinish is called after each step completes (both APIs), with the
// finished Step. Errors are not returned from the callback.
OnStepFinish func(step Step)
```

Helper (parity with Vercel `stepCountIs`): `func StepCountIs(n int) func([]Step) bool`.

Tests: StopWhen stops before MaxSteps; StopWhen default cap 16 documented+enforced (mock loop of 20 tool-call responses stops at 16); PrepareStep swaps ToolChoice on step 2 (assert recorded Calls); OnStepFinish invoked once per step in both GenerateText and StreamText with correct step data.

- [ ] **Step 1: Failing tests → implement in both loops → pass. Full check suite. Commit** — `feat: StopWhen, PrepareStep, OnStepFinish loop controls`

---

### Task 5: SmoothStream + SourcePart + Google grounding

**Files:**
- Create: `ai/smooth.go`
- Modify: `provider/message.go` (+`SourcePart`), `provider/stream.go` (+`SourcePart` passthrough as stream part `SourceDelta`? — NO: sources arrive whole; add stream part `SourceEvent{Source SourcePart}`), `internal/geminicompat/{wire.go,language_model.go}` (groundingMetadata → sources), `ai/generate_text.go` (+`Step.Sources`, `GenerateTextResult.Sources`), `ai/stream_text.go` (accessor)
- Test: each touched package

**Produces:**

```go
// provider: SourcePart{ID, URL, Title string} (content part; also carried in
// streams via SourceEvent{Source SourcePart}).
// geminicompat: candidates[0].groundingMetadata.groundingChunks[].web{uri,title}
// → SourcePart per chunk (ID = "source_<index>"); streaming chunks carrying
// groundingMetadata emit SourceEvent parts. No other provider emits sources
// in this wave (anthropic citations documented as future work).
// ai: Step.Sources []provider.SourcePart; GenerateTextResult.Sources (last
// step); TextStream.Sources() accumulated.

// ai/smooth.go
type SmoothOpts struct {
    Chunking string        // "word" (default) | "line"
    Delay    time.Duration // 0 = no artificial delay (default 10ms)
}
// SmoothStream re-chunks TextDeltas of the inner sequence into word- or
// line-sized deltas, sleeping Delay between emissions (skipped when 0 — and
// tests use 0). Non-text parts pass through unchanged, flushing any buffered
// text first. Trailing buffered text flushes at stream end.
func SmoothStream(parts iter.Seq[provider.StreamPart], opts SmoothOpts) iter.Seq[provider.StreamPart]
```

Note on Delay default: document "default 10ms when Delay is zero-valued AND Chunking=="" sentinel"? NO — keep it simple and predictable: Delay zero means NO delay; there is no implicit default delay. (Divergence from Vercel's 10ms default, documented.)

Tests: word/line chunking correctness incl. multi-word deltas and words split across deltas; non-text passthrough flush ordering; geminicompat grounding fixture → sources in Generate and Stream; ai accessors.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: SmoothStream, source parts, Google grounding`

---

### Task 6: imagesniff dedup + docs

**Files:**
- Create: `internal/imagesniff/imagesniff.go` (move the duplicated sniffer from openaicompat + geminicompat; both import it)
- Modify: `README.md`, `docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md`
- Test: `internal/imagesniff/imagesniff_test.go` (moved cases)

**Docs work:** README gains a "Core features" subsection listing wave-5 additions (ProviderOptions with the namespaced convention + example, reasoning + thinking example, middlewares, registry, loop controls, SmoothStream, sources, CosineSimilarity); ProviderOptions "reserved" line replaced (Task 1 removed the doc comments; this task fixes README). Spec gains "## Core parity (wave 5, shipped)" section. Verify all claims against code; table cell counts.

- [ ] **Step 1: Move sniffer + tests; both callers updated. Step 2: docs. Full check suite. Commit** — `refactor: shared image sniffing; docs: wave 5 core parity`

---

## Self-Review Notes

- **Vercel parity ledger:** providerOptions→T1; reasoning (+usage details)→T2; wrapLanguageModel middlewares (extractReasoning/simulateStreaming/defaultSettings)→T3; createProviderRegistry→T3; cosineSimilarity→T3; stopWhen/prepareStep/onStepFinish→T4; smoothStream→T5; sources→T5 (google only, documented). Remaining known gaps after wave 5: MCP client + telemetry (wave 6), anthropic citations, generative UI (out of scope).
- **Type consistency:** `ReasoningPart/ReasoningDelta/SourcePart/SourceEvent` produced in T2/T5 provider files and consumed by ai in the same tasks; middleware/registry/similarity self-contained in T3; loop controls self-contained in T4.
- **Breaking-change audit:** all additions are new fields (struct literals unaffected), new types, new methods — no signature changes.
