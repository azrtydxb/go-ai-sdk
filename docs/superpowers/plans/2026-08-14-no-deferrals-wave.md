# No-Deferrals Wave Implementation Plan (v0.4.0)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every item deferred out of the v0.3.0 wave: per-call Headers on all embedding/media/rerank paths, panic recovery for every Tool implementation, EmbedMany concurrency, StreamObject/partial-output performance and correctness polish, and the small MCP/test/doc minors.

**Architecture:** A shared `internal/httpheader` helper replaces six duplicated header loops and becomes the single application point for ~41 media/embed request sites. `Headers map[string]string` is added to every provider call struct and plumbed from the ai layer exactly like ProviderOptions. Panic recovery moves up to the tool loop (`executeToolCall` + ApprovalRequired sites) so caller-implemented Tools are covered. A shared partial-output tracker unifies stream_object and stream_text partial parsing with a repaired-string short-circuit.

**Tech Stack:** Go stdlib only (root module stays zero-dependency).

**Spec:** This plan is the spec; it derives from the v0.3.0 wave's ledgered deferrals (recorded in CHANGELOG v0.3.0 Notes + the final-review triage) and the 2026-08-14 request-path survey included inline below. Pascal's directive: nothing stays deferred.

## Global Constraints

- Root module MUST stay zero-dependency (stdlib only).
- Follow existing comment density/idiom; doc comments on every exported symbol.
- Every task: `go build ./... && go vet ./...` clean, `go test ./<touched pkgs>/...` green before commit; full `go test ./...` in the final task.
- Commit messages: conventional commits ending with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Headers precedence contract (same as language path): applied after auth; a header whose name case-insensitively equals the provider's auth header is SKIPPED (auth wins). Bedrock keeps its signed/unsigned split.
- Headers DO apply to async poll/download follow-up requests (same host/auth; consistency) and to WebSocket handshakes for streaming transcription. They do NOT apply to `internal/gauth` token exchange or `internal/fetchmedia` URL downloads.
- Design note allowed (not a deferral): StreamObject's repair-based partial parsing remains O(n) scan per delta by construction; this wave removes the redundant copies and re-decodes (short-circuit), and documents the remaining cost as inherent to the repair approach.

## Survey Findings (2026-08-14, authoritative for Tasks 1-4)

- Call structs (no Headers today): `provider/model.go:30` EmbeddingCall{Values, ProviderOptions}; `provider/image.go:15` ImageCall; `provider/speech.go:6` SpeechCall; `provider/transcription.go:9` TranscriptionCall; `provider/transcription_stream.go:9` StreamTranscriptionCall; `provider/translation.go:9` TranslationCall; `provider/video.go:16` VideoCall; `provider/rerank.go:9` RerankCall; `provider/files.go:9` FileUploadCall.
- Embedding is bare-args on the base interface; only openaicompat, geminicompat, voyage, vertex implement `EmbeddingModelWithOptions.EmbedCall`. cohere, mistral, bedrock embeddings need `EmbedCall` added to receive headers.
- Language-path header loops duplicated at: `internal/openaicompat/openaicompat.go:73` (applyExtraHeaders), `internal/geminicompat/geminicompat.go:52`, `providers/anthropic/language_model.go:89` (inline, plus headerIsSet at :55), `providers/cohere/language_model.go:51`, `providers/mistral/language_model.go:51`; bedrock's split variant at `providers/bedrock/language_model.go:105-127`.
- Media/embed request sites (41 total; 32 primary + 9 poll/download): openaicompat embedding.go:51/image.go:91/speech.go:82/transcription.go:135/translation.go:98; geminicompat embedding.go:55/image.go:86; voyage embedding.go:46 + rerank.go:43; cohere embedding.go:35 + rerank.go:43; mistral embedding.go:30; mixedbread rerank.go:68; elevenlabs speech.go:88 + transcription.go:83; deepgram transcription.go:105; fal video.go:72 + image.go:104; luma video.go:77,137 + image.go:76,137; vertex embedding.go:119; bedrock embedding.go:64 (passes nil headers today, comment says "not implemented this wave"); bfl image.go:2 sites; gladia transcription.go:159,207,250; assemblyai transcription.go:104,157,200; revai transcription.go:166,226,276; replicate video.go:2 sites; plus every other provider found by `grep -rn "http.NewRequest" providers/ internal/ | grep -v _test` that is a media/embed/rerank path. WebSocket handshakes: `providers/deepgram/live.go:47`, `providers/openai/realtime_transcription.go:43`.
- ai-layer copy sites (ProviderOptions precedent): embed.go:105-113 (embedCall gate must widen to `len(providerOptions) > 0 || len(headers) > 0`), generate_image.go:63, generate_speech.go:63, generate_video.go:59, transcribe.go:62, translate.go:56, rerank.go:76, upload_file.go:59.
- Gap-doc spots to update: `provider/call.go:108-111`, `providers/bedrock/embedding.go:62-63`, `docs/core/generating-text.md:69`.

---

### Task 1: internal/httpheader helper + Headers on all provider call structs

**Files:**

- Create: `internal/httpheader/httpheader.go`, `internal/httpheader/httpheader_test.go`
- Modify: `provider/model.go`, `provider/image.go`, `provider/speech.go`, `provider/transcription.go`, `provider/transcription_stream.go`, `provider/translation.go`, `provider/video.go`, `provider/rerank.go`, `provider/files.go`, `provider/call.go` (gap doc), and the five duplicated language-path loops (openaicompat, geminicompat, anthropic, cohere, mistral) refactored to call the helper. Bedrock's split variant stays in place (add a doc cross-reference only).

**Interfaces:**

- Produces: `func Apply(req *http.Request, headers map[string]string, authHeaderName string)` in package `httpheader` — sets each k,v via `req.Header.Set` EXCEPT keys case-insensitively equal to authHeaderName (skipped; auth wins). Empty authHeaderName means no skip. Also `Headers map[string]string` field added to all nine call structs, doc comment on each: "Extra HTTP headers applied to the request(s) this call makes, after auth; a key matching the provider's auth header is ignored. Same contract as Call.Headers."
- Consumes: nothing from other tasks.

- [ ] **Step 1: Failing tests** for Apply: sets headers; skips auth header case-insensitively; nil map no-op; empty authHeaderName applies everything.
- [ ] **Step 2:** RED run. **Step 3:** Implement Apply (~10 lines + doc). **Step 4:** GREEN.
- [ ] **Step 5:** Refactor the five language-path loops to `httpheader.Apply(req, headers, cfg.authHeaderName())` equivalents — behavior-preserving (anthropic keeps its headerIsSet/anthropic-beta logic around the call). Run each touched package's tests.
- [ ] **Step 6:** Add the Headers field to the nine call structs with the doc comment above. Update `provider/call.go:108-111` to state the new scope: "Implemented by every language-model request path and (since v0.4.0) every embedding/media/rerank/file request path via the per-modality call structs' Headers field."
- [ ] **Step 7:** `go build ./... && go vet ./... && go test ./internal/httpheader/ ./provider/ ./internal/openaicompat/ ./internal/geminicompat/ ./providers/anthropic/ ./providers/cohere/ ./providers/mistral/` green. **Commit** `feat(provider): Headers on all modality call structs; shared httpheader.Apply helper`

---

### Task 2: ai-layer Headers plumbing

**Files:**

- Modify: `ai/embed.go`, `ai/generate_image.go`, `ai/generate_speech.go`, `ai/generate_video.go`, `ai/transcribe.go`, `ai/translate.go`, `ai/rerank.go`, `ai/upload_file.go`, `docs/core/generating-text.md:69`
- Test: each file's existing `_test.go`

**Interfaces:**

- Consumes: Task 1's call-struct Headers fields.
- Produces: `Headers map[string]string` on EmbedOpts, EmbedManyOpts, GenerateImageOpts, GenerateSpeechOpts, GenerateVideoOpts, TranscribeOpts, TranslateOpts, RerankOpts, UploadFileOpts — doc comment mirroring GenerateTextOpts.Headers (`ai/options.go:105-109`), copied into the call next to ProviderOptions at each copy site listed in the survey. Embed special case: widen `embedCall`'s dispatch gate to `len(providerOptions) > 0 || len(headers) > 0` and pass Headers in EmbeddingCall — a model without EmbedCall support silently ignores them (document exactly that, matching ProviderOptions' existing "silently ignored" wording).

- [ ] **Step 1:** Failing tests: for one modality with a call-struct interface (image), mock model asserts `call.Headers` arrives; for Embed, a mock implementing EmbeddingModelWithOptions asserts EmbeddingCall.Headers arrives even when ProviderOptions is empty (the widened gate); for Embed with a plain EmbeddingModel, Headers set + no panic (silently ignored).
- [ ] **Step 2:** RED. **Step 3:** Implement all nine opts + copy sites. **Step 4:** GREEN, full `go test ./ai/`.
- [ ] **Step 5:** Update the docs table row (`docs/core/generating-text.md:69`). **Commit** `feat(ai): per-call Headers for embed, media, rerank, and file upload`

---

### Task 3: Headers application — compat internals + bedrock/vertex (shared-family sites)

**Files:**

- Modify: `internal/openaicompat/embedding.go, image.go, speech.go, transcription.go, translation.go`; `internal/geminicompat/embedding.go, image.go`; `providers/bedrock/embedding.go` (pass call.Headers instead of nil; delete the "not implemented this wave" comment); `providers/vertex/embedding.go`
- Test: each package's tests (they use httptest fake servers — assert the header arrives on the wire).

**Interfaces:**

- Consumes: Task 1's httpheader.Apply and call-struct fields.
- Produces: every listed site calls `httpheader.Apply(req, call.Headers, <authHeaderName>)` after auth is set. openaicompat uses `cfg.authHeaderName()`; geminicompat mirrors what its language path skips; vertex/bedrock: bedrock routes through its existing doRequest header partitioning (signed/unsigned split now fed call.Headers); vertex applies after its gauth token header with that header name as the skip.

- [ ] **Step 1 per site-family:** failing wire-level test (httptest server asserts `X-Test: yes` present; auth-named key in Headers does NOT override real auth). **Step 2:** RED. **Step 3:** Apply at each of the 10 sites. **Step 4:** GREEN per package.
- [ ] **Step 5:** `go test ./internal/... ./providers/bedrock/ ./providers/vertex/` green. **Commit** `feat(providers): apply per-call Headers in openaicompat/geminicompat internals, bedrock and vertex embeddings`

---

### Task 4: Headers application — all remaining provider sites (+ cohere/mistral/bedrock EmbedCall, WS handshakes)

**Files:**

- Modify: every remaining media/embed/rerank request site from the survey: voyage (embedding.go:46, rerank.go:43), cohere (embedding.go — ALSO add `EmbedCall` implementing EmbeddingModelWithOptions, delegating to the shared request builder with ProviderOptions+Headers; rerank.go:43), mistral (embedding.go:30 + add EmbedCall), mixedbread (rerank.go:68), elevenlabs (speech.go:88, transcription.go:83), deepgram (transcription.go:105), fal (video.go:72, image.go:104), luma (video.go:77+poll :137, image.go:76+poll :137), bfl (image.go both sites), gladia (transcription.go:159,207,250), assemblyai (transcription.go:104,157,200), revai (transcription.go:166,226,276), replicate (video.go both sites), plus any other media/embed/rerank site surfaced by `grep -rn "http.NewRequest" providers/ | grep -v _test` not already covered (prodia, hume, lmnt, minimax, deepinfra, gladia…: check each grep hit; language-model, file-store, gauth, fetchmedia sites are out of scope). WebSocket: `providers/deepgram/live.go:47` and `providers/openai/realtime_transcription.go:43` pass StreamTranscriptionCall.Headers into the WS handshake headers (find how internal/websocket accepts handshake headers; auth-skip contract applies).
- Test: per provider, extend an existing httptest-based test to assert one header arrives (one test per provider is enough; polls covered in luma/gladia/assemblyai/revai/replicate tests where a poll already happens in-test).

**Interfaces:** Consumes Tasks 1-2. Produces: no new API beyond cohere/mistral `EmbedCall`.

- [ ] **Step 1:** Enumerate ALL grep hits into a checklist in your report file first — every media/embed/rerank hit must end the task either patched or explicitly listed as out-of-scope with its category. Silent omission is a spec failure.
- [ ] **Step 2-4:** TDD per provider (RED wire test → apply → GREEN). Poll/follow-up requests get the same Headers.
- [ ] **Step 5:** Full `go test ./providers/...` green. **Commit** `feat(providers): apply per-call Headers across all media, embedding, and rerank request paths`

---

### Task 5: Panic recovery for every Tool implementation (+ ApprovalRequired hooks)

**Files:**

- Modify: `ai/generate_text.go` (`executeToolCall` ~:561/585; ApprovalRequired call sites ~:587,652), `ai/tool.go` (keep the existing NewTool recover; adjust comment to note the loop-level guard now also covers non-NewTool Tools), `CHANGELOG.md` (v0.4.0 entry may now claim all Tool implementations + approval hooks)
- Test: `ai/generate_text_test.go` (or tool_test.go)

**Interfaces:**

- Produces: an unexported helper `func recoverToolPanic(toolName string, fn func() (any, error)) (res any, err error)` (name flexible) that runs fn and converts a panic to `*ToolExecutionError{ToolName, Cause: fmt.Errorf("tool panicked: %w|%v", …), Stack: debug.Stack()}` — same shape as Task-1-of-v0.3.0's recover. `executeToolCall` wraps `t.Execute` in it; the ApprovalRequired invocations are wrapped likewise (a panicking approval hook fails THAT tool call with a ToolExecutionError, batch semantics otherwise unchanged — the error routes exactly as an Execute error does at that site).

- [ ] **Step 1:** Failing tests: (a) a caller-implemented `ai.Tool` (hand-rolled struct, not NewTool) whose Execute panics → GenerateText returns/records `*ToolExecutionError` with Stack, process doesn't crash; (b) an ApprovalRequirer whose ApprovalRequired panics → same conversion, other calls in the batch unaffected (assert via ToolResultRecord/err surface — mirror how an Execute error is asserted in existing tests); (c) existing NewTool panic tests still green.
- [ ] **Step 2:** RED. **Step 3:** Implement helper + wrap the three sites. **Step 4:** GREEN, full `go test ./ai/ ./agent/`.
- [ ] **Step 5:** **Commit** `fix(ai): recover panics from any Tool implementation and ApprovalRequired hooks`

---

### Task 6: Partial-output unification, ID-keyed tap, fence-tolerant partials, StreamObject perf

**Files:**

- Create: `ai/partial_tracker.go` (unexported)
- Modify: `ai/stream_object.go` (use tracker), `ai/stream_text.go` (use tracker; key tool-mode tap to the FIRST output-named tool call's ID — later same-named calls don't feed the tap), `ai/output.go` (move arrayOutput.decodePartial after decode; add prefix-fence stripping so partials fire for fence-wrapping models in ALL schema modes; drop the ineffective full-stripFences from jsonOutput.decodePartial in favor of the same prefix stripper)
- Test: `ai/stream_object_test.go`, `ai/stream_text_output_test.go`

**Interfaces:**

- Produces: unexported `type partialTracker struct` with `func (t *partialTracker) feed(delta []byte, decode func(string) (any, bool)) (any, bool)`: appends to an internal `strings.Builder`; short-circuits when the partialjson.Repair output equals the previous repaired string (skip decode + DeepEqual entirely); otherwise decodes and DeepEqual-dedupes, returning (value, true) only for a NEW distinct partial. Also unexported `stripPartialFences(s string) string`: if the accumulated text starts with a ``` fence line, drop that line; drop a trailing fence if present — safe on incomplete streams (prefix-only, no requirement of a closing fence).
- Consumes: `internal/partialjson.Repair` (unchanged).

**Behavior spec:**

1. stream_object and stream_text both route partial parsing through partialTracker — one implementation, two users; existing observable behavior (which partials fire) unchanged except where 2-4 below improve it.
2. Repaired-string short-circuit: a delta that doesn't change the repaired JSON (e.g. trailing whitespace) costs no unmarshal and no DeepEqual. This closes the deferred "P8/P10" item to the extent possible without an incremental parser; document the residual O(n) scan per delta as inherent (design note in partial_tracker.go's package comment).
3. Tool-mode tap keying: on the first ToolCallDelta whose call is (or ends up) the output tool, record that call ID; only deltas for that ID feed the tracker. A second same-named call's deltas are ignored by the tap (Output() already decodes first-match) — restoring the documented "last partial equals final output" guarantee.
4. Fence tolerance: partials now fire for models that wrap JSON in ``` fences, in object/array/json modes (choice still never fires).

- [ ] **Step 1:** Failing tests: (a) stream_object emits identical partial sequence as before for the existing fixtures (regression harness — reuse existing tests) plus a new test that a whitespace-only delta triggers no new partial callback and (assert via a decode-counting hook or by callback count) no extra emission; (b) stream_text tool-mode with TWO same-named output tool calls: partials track the first call only, last partial DeepEquals Output(); (c) fenced stream ("```json\n{..." deltas) yields partials in object mode; (d) arrayOutput method order compiles (trivial).
- [ ] **Step 2:** RED. **Step 3:** Implement tracker + rewire both streams + fence stripper + ordering fix. **Step 4:** GREEN, full `go test ./ai/`.
- [ ] **Step 5:** **Commit** `perf(ai): shared partial-output tracker with repair short-circuit; ID-keyed tool-mode tap; fence-tolerant partials`

---

### Task 7: EmbedMany concurrency

**Files:**

- Modify: `ai/embed.go`
- Test: `ai/embed_test.go`

**Interfaces:**

- Produces: `EmbedManyOpts.Concurrency int` — max batches in flight; 0 or 1 = sequential (existing behavior, default unchanged). >1 fans batches out over a worker pool (sync.WaitGroup + buffered-channel semaphore, stdlib only); results reassembled index-aligned; Usage summed; first error wins (context for remaining batches cancelled via context.WithCancel; in-flight batches drain). Callback contract documented: with Concurrency > 1, OnEmbedStart/OnEmbedEnd fire per batch from worker goroutines — order is completion order, not batch order, and callbacks must be goroutine-safe; sequential mode keeps the existing in-order guarantee verbatim.

- [ ] **Step 1:** Failing tests: (a) Concurrency=4 over 8 batches returns index-aligned embeddings identical to sequential run (mock model records per-call values; use a batch-size-1 model); (b) error in batch 3 → EmbedMany returns that error (translated), remaining batches cancelled (assert ≤ expected call count via atomic counter); (c) Concurrency unset → call order strictly sequential (existing tests keep passing); (d) callbacks fire once per batch under concurrency (atomic count == batch count).
- [ ] **Step 2:** RED. **Step 3:** Implement. **Step 4:** GREEN incl. `-race`. **Step 5:** **Commit** `feat(ai): EmbedMany batch concurrency via Concurrency option`

---

### Task 8: MCP + test + doc minors batch

**Files:**

- Modify: `mcp/elicitation.go` + `mcp/roots.go` (single-lock roots/list: one mu acquisition covering the gate check and the roots read — e.g. dispatch reads both under one lock and passes the snapshot to the handler), `mcp/sampling.go` + `mcp/elicitation.go` (extract the duplicated wire-message copy loop into one unexported helper used by both), `mcp/client_test.go` (rename `TestCallToolConcatenatesTextAndIgnoresOtherTypes` → `TestCallToolConcatenatesTextIntoTextField`), `internal/schema/schema_test.go` (add concurrent-convergence test: N goroutines race `ForType` on a fresh type, all get identical backing bytes — run with `-race`), `docs/mcp.md` (add a `## Notifications` section describing NotificationHandler, best-effort bounded delivery, install-before-Initialize), `CHANGELOG.md` (point the v0.3.0 NotificationHandler entry at the new `#notifications` anchor)
- Test: as listed.

**Interfaces:** no new exported API.

- [ ] **Step 1:** Failing/changed tests first where applicable (schema concurrency test is new; roots single-lock keeps existing tests green; rename is mechanical). **Step 2-4:** implement, GREEN per package (`go test ./mcp/ ./internal/schema/ -race`).
- [ ] **Step 5:** **Commit** `refactor(mcp,schema,docs): close remaining v0.3.0 review minors`

---

### Task 9: CHANGELOG v0.4.0, docs, full verification

**Files:**

- Modify: `CHANGELOG.md` (new `## v0.4.0 (unreleased)` section: Headers everywhere, universal panic recovery, EmbedMany concurrency, partial-tracker perf + ID-keyed tap + fence tolerance, MCP/test minors; a Notes entry stating the ONLY remaining known cost: repair-based partial parsing scans the buffer per delta, inherent to the approach), `docs/core/embeddings-and-rerank*.md` or equivalent (Concurrency + Headers), `docs/core/media.md` (Headers), `docs/mcp.md` if touched rows remain, `README.md` if it mentions the headers gap.
- [ ] **Step 1:** Write CHANGELOG + doc updates; sweep `grep -rn "not implemented\|this wave\|silently ignored" docs/ provider/ ai/ | grep -i header` for stragglers.
- [ ] **Step 2:** `go build ./... && go vet ./... && go test ./...` ALL green.
- [ ] **Step 3:** **Commit** `docs: v0.4.0 changelog and headers/concurrency documentation`

---

## Self-Review Notes

- Coverage vs the deferred list: Headers gap→T1-T4; recovery scope→T5; ID-agnostic tap→T6; StreamObject O(n²)/DeepEqual→T6 (short-circuit + design note); EmbedMany sequential→T7; roots double-lock, sampling copy-loop dup, stale test name, schema concurrency test, Notifications anchor→T8; arrayOutput ordering + stripFences asymmetry→T6. Stack-in-string and Output() masking were already fixed in v0.3.0's final fix wave. The empty `## [Unreleased]` head stays (Keep-a-Changelog convention, standing section).
- Order matters: T1 → T2 → T3 → T4 (Headers chain); T5-T8 independent of the chain and of each other; T9 last.
- Type consistency: `httpheader.Apply`, `partialTracker.feed`, `stripPartialFences`, `EmbedManyOpts.Concurrency`, cohere/mistral `EmbedCall` — defined once each, referenced consistently.
