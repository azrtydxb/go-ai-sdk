# go-ai-sdk Wave 14 Implementation Plan (observability + final v6 gaps + docs re-baseline)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the OpenTelemetry bridge as a nested zero-root-dep module, close the four small remaining AI SDK 6 core gaps (tool `inputExamples` + middleware, tool input streaming lifecycle hooks, tool `strict` flag, structured `timeout`), re-baseline the docs corpus to v6, and cut CHANGELOG v0.2.0 with a final gap-audit record. This is the last wave of the v6 parity program.

**Architecture:** The OTel bridge lives in `contrib/otel/` with its own `go.mod` (may depend on `go.opentelemetry.io/otel`), keeping the root module zero-dependency. To let the bridge parent spans correctly, the root `ai.Telemetry` seam gains a `context.Context` on span start and a stable `CorrelationID` on `SpanInfo` (a small, documented pre-1.0 breaking change). The four v6 gaps are additive fields/callbacks on the existing tool + options surfaces and one new middleware.

**Tech Stack:** Go 1.26, stdlib only in the root module; `contrib/otel` may use the OTel libraries.

## Global Constraints

- Root module `github.com/azrtydxb/go-ai-sdk` stays **zero-dependency** — verify `go.mod` has no `require` after this wave. Only `contrib/otel/go.mod` carries external deps.
- ADDITIVE on exported surfaces EXCEPT the one sanctioned `ai.Telemetry` signature change (Task 1) — flagged as a breaking change in the CHANGELOG's v0.2.0 breaking-changes note.
- Providers never retry; ctx passthrough; ProviderOptions conventions unchanged.
- `provider/providertest` untouched.
- Full check suite per commit for the ROOT module: `go vet ./... && go build ./... && go test ./... && gofmt -l .` (+`-race` on `ai` when touched). For `contrib/otel`: run its own `go test ./...` from inside `contrib/otel` (separate module — the root `./...` does NOT include it; CI/docs must note both).
- Commits conventional, trailer:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Telemetry seam enhancement for OTel (context + correlation id)

**Files:**
- Modify: `ai/telemetry.go` (interface + SpanInfo + middleware), `ai/telemetry_test.go`

**Change (sanctioned breaking change on a pre-1.0 API):**
```go
type SpanInfo struct {
	CorrelationID string // NEW: stable unique id, identical on the OnSpanStart and OnSpanEnd for one call; lets a bridge map end→start without StartTime collisions
	Operation     string
	ModelID       string
	ProviderName  string
	StartTime     time.Time
	EndTime       time.Time
	Usage         provider.Usage
	FinishReason  provider.FinishReason
	Err           error
}

type Telemetry interface {
	// OnSpanStart now receives the ctx of the underlying model call, so a
	// bridge can read the parent span from ctx (OTel) and attach the new
	// span as its child. The returned ctx is currently unused by the SDK
	// (the provider call is a leaf); keep the signature ctx-in only.
	OnSpanStart(ctx context.Context, info SpanInfo)
	OnSpanEnd(info SpanInfo)
}
```
Middleware: generate a CorrelationID per call (monotonic counter + a per-middleware random-ish prefix — but NO Math.random/time-based nondeterminism restriction applies here since this is runtime, not a workflow script; use `crypto/rand` for a short prefix once at middleware construction, then an atomic counter — or simplest: an atomic int64 counter formatted as a string, prefixed with the middleware's own pointer-derived id is overkill; a package-level atomic counter is sufficient and testable). Set the SAME CorrelationID on both the start and end SpanInfo for a given call. Pass `ctx` to `OnSpanStart` (Generate has it directly; Stream has it — thread it into the telemetryStream so the deferred end can't but the start can — start fires before wrapping, so ctx is in scope). The stream end path (`OnSpanEnd`) keeps no ctx (matches the "end has no ctx" reality; the CorrelationID carries the linkage).

Existing behavior preserved: OnSpanStart still fires before the call with Usage/FinishReason zero; OnSpanEnd still fires once (idempotent) with EndTime set + Usage/FinishReason xor Err. The stream lifecycle edge cases (FinishPart / abandon / exhaust-without-finish / Close) are unchanged except each now carries the matching CorrelationID.

**Tests:** CorrelationID identical across the start/end pair for one Generate call and one Stream call; two concurrent calls get distinct CorrelationIDs (atomic counter, -race); OnSpanStart receives a non-nil ctx carrying a test value put on the call's ctx; all existing telemetry_test.go assertions updated for the new signature and still green.

- [ ] **Step 1: Update tests to new signature → implement → green. Full check suite (-race on ai). Commit** — `feat(telemetry)!: ctx on OnSpanStart and CorrelationID on SpanInfo for OTel bridging`

---

### Task 2: contrib/otel nested module — GenAI-semconv bridge

**Files:**
- Create: `contrib/otel/go.mod`, `contrib/otel/go.sum`, `contrib/otel/otel.go`, `contrib/otel/otel_test.go`, `contrib/otel/doc.go`, `contrib/otel/README.md`

**Module:** `module github.com/azrtydxb/go-ai-sdk/contrib/otel`, `go 1.26`, requiring `go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/trace`, and (test-only) `go.opentelemetry.io/otel/sdk` for the in-memory span recorder. Use a `replace github.com/azrtydxb/go-ai-sdk => ../..` directive so it builds against the local root during development (document that published consumers get it via the tagged root version — the replace is dev-only; keep it but note it; alternatively pin the root require to the v0.2.0 version — use the replace for now since we tag root and contrib together).

**Interfaces (Produces):**
```go
package otel

// Bridge implements ai.Telemetry by emitting OpenTelemetry GenAI-semantic-
// convention spans on a Tracer.
type Bridge struct{ /* unexported: tracer, in-flight span map keyed by CorrelationID, mutex */ }

type Option func(*Bridge)
func WithTracer(t trace.Tracer) Option        // default: otel.Tracer("github.com/azrtydxb/go-ai-sdk")
func WithSpanNamePrefix(prefix string) Option  // default "" → span name is the gen_ai operation

// New returns a Bridge usable as ai.Telemetry:
//   model = ai.TelemetryMiddleware(base, otel.New())
func New(opts ...Option) *Bridge

func (b *Bridge) OnSpanStart(ctx context.Context, info ai.SpanInfo)
func (b *Bridge) OnSpanEnd(info ai.SpanInfo)
```
Span mapping (GenAI semconv, stable attributes as of the OTel semconv the pinned lib version ships — use the string keys directly to avoid coupling to a semconv package version; document the keys):
- Span name: `<prefix>` + `"chat "` + ModelID (operation "generate"/"stream" → the gen_ai op is "chat"); kind = Client.
- Start attributes: `gen_ai.operation.name` = "chat"; `gen_ai.system` = ProviderName; `gen_ai.request.model` = ModelID.
- On end: `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` from Usage; `gen_ai.response.finish_reasons` = []string{string(FinishReason)} when non-empty; on Err → span.RecordError(err) + status Error; else status Ok. End the span.
- Correlation: OnSpanStart starts the span via `tracer.Start(ctx, name, ...)` and stores it under `info.CorrelationID` in the map; OnSpanEnd looks it up, sets end attributes, ends it, deletes the entry. A missing entry on end (shouldn't happen) is ignored defensively. Mutex-guard the map (-race).
- Parenting: `tracer.Start(ctx, ...)` automatically parents under any span already in ctx — this is why Task 1 added the ctx.

**Tests** (using `go.opentelemetry.io/otel/sdk/trace` + `tracetest.NewSpanRecorder`): a Generate through TelemetryMiddleware+Bridge produces one ended span with the right name/attributes/status; error call → span status Error + recorded error; a stream call → one span ending on FinishPart with usage; parenting — start a parent span, run a bridged call with that ctx, assert the child's parent span id matches; concurrent calls → distinct spans, no race, correct attribute isolation. Run `cd contrib/otel && go test -race ./...`.

- [ ] **Step 1: go.mod + bridge + tests → green (run from inside contrib/otel). Root suite still green + zero-dep. Commit** — `feat(contrib/otel): OpenTelemetry GenAI-semconv bridge as a nested module`

---

### Task 3: Tool inputExamples + AddToolInputExamplesMiddleware + strict flag

**Files:**
- Modify: `ai/tool.go` (Tool interface/impl + NewTool), `ai/middleware.go` (new middleware), `ai/generate_text.go`/`ai/options.go` (thread strict + examples into the wire ToolDef), `provider/call.go` (ToolDef gains Strict + InputExamples), the six ToolDef-serializing converters where strict maps to the wire
- Test: `ai/tool_test.go`, `ai/middleware_test.go`

**Changes:**
1. `provider.ToolDef` gains (ADDITIVE): `Strict bool` and `InputExamples []json.RawMessage`. Wire mapping: openaicompat → `"strict": true` inside the `function` object when Strict (OpenAI strict function calling); anthropic/geminicompat/bedrock/cohere/mistral → Strict currently unsupported on the wire → ignored with a comment (document per family; only openaicompat carries it today). InputExamples is NOT a wire field on any provider except Anthropic's `input_examples` (anthropic tool object) — map it there; elsewhere it's consumed by the middleware (below), not sent raw.
2. `ai.Tool` interface gains `Strict() bool` and `InputExamples() []json.RawMessage` (with a doc note that most tools return false/nil). `NewTool[Args]` gains functional options: `WithToolStrict()` and `WithToolInputExamples(examples ...Args)` (typed examples marshaled to json.RawMessage at construction; panic on marshal failure like schema derivation). Keep `NewTool(name, desc, fn)` working — add a variadic `...ToolOption` parameter (ADDITIVE: `func NewTool[Args any](name, description string, fn func(...)..., opts ...ToolOption) Tool`). Verify no existing caller breaks (variadic addition is source-compatible).
3. `AddToolInputExamplesMiddleware(model) provider.LanguageModel`: for each tool in the outgoing Call that has InputExamples but the target provider lacks native support (i.e. always, except we can't know the provider here — so: always append a compact `"\n\nExample inputs:\n"` + each example JSON on its own line to the tool's Description, and CLEAR InputExamples so a native-supporting provider downstream doesn't double-count). This mirrors v6's middleware that serializes examples into description text for providers without native support. Idempotent per call (operates on a copy of the Call's Tools).

**Tests:** NewTool with WithToolStrict → Tool.Strict() true, wire ToolDef has strict:true for openaicompat, absent for anthropic; WithToolInputExamples → examples marshaled correctly, appear in Anthropic's input_examples wire field; AddToolInputExamplesMiddleware appends examples to description text and clears the field (assert the modified Call the inner model sees); existing NewTool callers unaffected (compile + behavior).

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on ai). Commit** — `feat: tool strict flag, inputExamples, and AddToolInputExamplesMiddleware`

---

### Task 4: Tool input streaming lifecycle hooks

**Files:**
- Modify: `ai/options.go` (per-tool hook fields on the Tool or on GenerateTextOpts — decide below), `ai/stream_text.go` (fire during tool-call delta streaming), `ai/tool.go`
- Test: `ai/stream_text_test.go` (new cases)

**Design:** v6 fires `onInputStart`/`onInputDelta`/`onInputAvailable` per tool as the model streams that tool call's arguments. In Go, put these on the Tool (matching v6's per-tool placement) via NewTool options, since they're tool-scoped:
```go
type ToolInputCallbacks struct {
	OnInputStart     func(ctx context.Context, toolCallID string)
	OnInputDelta     func(ctx context.Context, toolCallID, delta string) // raw args-JSON text delta
	OnInputAvailable func(ctx context.Context, toolCallID string, input json.RawMessage) // fully assembled, pre-Execute
}
func WithToolInputCallbacks(cb ToolInputCallbacks) ToolOption
```
StreamText wiring: the provider stream already yields tool-call argument deltas (check provider.StreamPart variants — ToolCallDelta / ToolCallPart; internal/partialjson assembles them). When a tool call's first arg delta for a given toolCallID arrives → OnInputStart(ctx, id); each subsequent delta → OnInputDelta(ctx, id, deltaText); when the tool call is complete (before runToolCalls executes it) → OnInputAvailable(ctx, id, fullArgs). GenerateText (non-streaming) fires only OnInputAvailable (no deltas exist) right before execution — document that Start/Delta are stream-only. Callbacks are nil-checked, fired synchronously on the consuming goroutine, and must not fire for the Output tool-mode synthetic call.

**Tests:** StreamText with a tool whose args stream in 3 deltas → OnInputStart once, OnInputDelta ×3 with concatenation == full args, OnInputAvailable once with the assembled JSON, all before Execute; GenerateText fires only OnInputAvailable; multiple tool calls in one step get correctly-keyed callbacks by toolCallID; -race with callbacks touching shared state.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on ai). Commit** — `feat: per-tool input streaming lifecycle hooks (start/delta/available)`

---

### Task 5: Structured timeout (total / per-step / stream-chunk-stall)

**Files:**
- Modify: `ai/options.go` (+Timeout field), `ai/generate_text.go` (per-step deadline), `ai/stream_text.go` (chunk-stall watchdog)
- Test: `ai/timeout_test.go` (new)

**Interfaces (Produces):**
```go
// Timeout bounds a GenerateText/StreamText run more finely than a single
// context deadline. Zero fields mean "no bound" for that dimension.
type Timeout struct {
	Total time.Duration // whole run (all steps); applied as a derived context deadline
	Step  time.Duration // each model call/step; derived per-step context deadline
	Chunk time.Duration // StreamText only: max gap between stream parts; a stall aborts the stream
}
```
`GenerateTextOpts` gains `Timeout *Timeout`.
Semantics:
- Total: wrap the run's ctx in `context.WithTimeout(ctx, Total)` at entry (both loops). Interacts with an existing ctx deadline — the earlier of the two wins (that's automatic with WithTimeout).
- Step: each step's model call gets its own `context.WithTimeout(stepCtx, Step)` derived from the run ctx; a step timeout surfaces as the step's error (classified as a timeout → OnAbort vs OnError? A deadline from OUR Step bound is an error, not a user abort — route to OnError with a `*TimeoutError`; document. But a Total/outer ctx cancel stays OnAbort). Add `type TimeoutError struct{ Dimension string; Limit time.Duration }` in ai/errors.go.
- Chunk (StreamText): a watchdog resets on every yielded provider.StreamPart; if no part arrives within Chunk, cancel the stream and end with a `*TimeoutError{Dimension:"chunk"}` via the error path (not abort). Implement with a timer reset per part in the Parts() pump; ctx-cancel the inner stream on fire; ensure no goroutine leak (stop the timer on normal end/Close).

**Tests:** Total shorter than a slow mock → run ends with TimeoutError(total); Step bound trips on a slow single step; Chunk stall (mock stream that delivers 2 parts then hangs) → TimeoutError(chunk) and the reader goroutine + watchdog both exit (goroutine-count check); a fast run under all bounds completes normally; Total interacts with a shorter user ctx (user ctx wins → ctx.Canceled/OnAbort, not TimeoutError); -race on the watchdog.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on ai). Commit** — `feat: structured Timeout (total, per-step, stream-chunk-stall)`

---

### Task 6: Docs re-baseline to v6 + CHANGELOG v0.2.0 + final audit record

**Files:**
- Modify: `docs/core/telemetry.md` (replace the OTel "sketch" with the real `contrib/otel` bridge — usage, span attributes, the CorrelationID/ctx change; keep the plain-interface story for non-OTel users), `docs/core/tools.md` (strict, inputExamples + AddToolInputExamplesMiddleware, the input streaming hooks), `docs/core/generating-text.md` + `docs/core/streaming.md` (Timeout), `docs/core/middleware-and-registry.md` (AddToolInputExamplesMiddleware row), `docs/migrating-from-vercel-ai-sdk.md` (BIG re-baseline: OTel bridge → Shipped as contrib/otel; the 4 gaps → Shipped; re-baseline the whole "AI SDK 6 delta" table to reflect full v6 core parity; note the Telemetry breaking change; keep v7-only features listed as "not targeted — v7"), `docs/architecture.md` (add the observability layer + the zero-root-dep + nested-module note; contrib/otel), `README.md` (feature list: OTel bridge, v6 parity statement, the new tool/timeout features), `docs/README.md` (link contrib/otel if a page is added; add a telemetry/OTel pointer), `CHANGELOG.md` (new `## [0.2.0]` release section — move everything from [Unreleased] waves 9-14 into it under Added/Changed/Fixed with the Telemetry breaking change called out under a `### Changed`/`### BREAKING` note; date it 2026-08-03; leave an empty [Unreleased]).
- Create: `docs/core/observability.md` OR fold into telemetry.md (decide — prefer expanding telemetry.md to avoid a thin new page; if it grows past ~250 lines, split). A `contrib/otel/README.md` already exists from Task 2 — link it.
- Create: `docs/superpowers/specs/2026-08-03-v6-parity-final-audit.md` — the final gap-audit record: what AI SDK 6 core surface is covered (the have-list), what's deliberately out of scope (UI/RSC/WebRTC/DevTools), and the confirmed v7-only items not targeted. This is the "we are done with v6" artifact.
- Verification discipline: snippets compile-verified (root snippets in-repo; the contrib/otel snippet built inside that module); every claim grepped; migration-table rows all resolve; CHANGELOG version/date correct; links resolve including cross-module ones.

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 14 — OTel bridge, v6 completeness, CHANGELOG v0.2.0, final audit`

---

## Self-Review Notes

- The `ai.Telemetry` signature change (Task 1) is the only breaking change in the whole v6 program — it's pre-1.0 and the only known implementer is the docs sketch. Call it out prominently in CHANGELOG v0.2.0's breaking-changes note; the migration is a one-line signature update.
- `contrib/otel` is a SEPARATE module: the root `go test ./...` will NOT run its tests. Task 2 and Task 6 must both state that `contrib/otel` is tested by `cd contrib/otel && go test ./...`. The final verify (before merge) runs BOTH.
- The 4 gaps came from the wave-14 gap re-audit (essentially-at-parity; these are the only genuine v6 core items). If any turns out larger than "small" during implementation, it still ships this wave (no deferrals per the standing orders) — but Task 4 (streaming hooks) is the one most likely to surface stream-loop subtleties; its reviewer should scrutinize the tool-call-delta routing and the Output-tool-mode exemption.
- After Task 6: tag v0.2.0 on the root (and the contrib/otel module shares the tag or gets contrib/otel/v0.2.0 per Go's nested-module tagging — decide at tag time; Go convention is `contrib/otel/v0.2.0` for the nested module path). The wrap-up + tag happen AFTER the wave merges to main.
- This completes the roadmap. The final-audit spec (Task 6) is the program's closing artifact.
