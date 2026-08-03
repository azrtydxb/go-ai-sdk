# go-ai-sdk Wave 10 Implementation Plan (Output modes, Rerank, unified Reasoning, lifecycle callbacks)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** AI SDK 6 structured-output modes on `GenerateText` (text/object/array/choice/json + `OutputAs[T]`), a first-class reranking surface (`provider.RerankingModel` + `ai.Rerank` + Cohere `/v2/rerank`), a unified `Reasoning` request option mapped per provider, and the v6 lifecycle-callback event set.

**Architecture:** Output modes generalize `generate_object.go`'s existing native-JSON/tool-mode machinery behind an `Output` interface field on `GenerateTextOpts`, with generic constructors and a generic `OutputAs[T]` accessor (opts stay non-generic). Rerank follows the Embed pattern exactly (interface in `provider/`, orchestration + retries in `ai/`, registry lookup, fixture-tested Cohere implementation). `Reasoning` is a new `provider.Call` field serialized by each provider's request builder with a documented effort→budget table. Lifecycle callbacks are new opts fields fired at the model-call and tool-execution boundaries in both loops.

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies; ADDITIVE only on existing exported surfaces.
- Providers never retry; non-2xx → `ai.NewAPICallError` (retryable 429/408/≥500); ctx passthrough everywhere.
- ProviderOptions convention unchanged: namespaced raw wire keys, shallow-merged LAST into the SDK-built body — **ProviderOptions win on key collision**, including over the new `Reasoning` field (roadmap wording "precedence over ProviderOptions" is superseded by this, for consistency with every other first-class field; document it).
- `provider/providertest` is LanguageModel-scoped and must NOT be modified this wave (no extension needed — rerank is fixture-tested in the provider package).
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` (add `-race` on `./ai/...` when ai is touched).
- Cohere rerank carries the "needs live testing" doc note like all providers.
- Commits conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: provider.RerankingModel + ai.Rerank + Cohere rerank + registry

**Files:**
- Create: `provider/rerank.go`, `ai/rerank.go`, `ai/rerank_test.go`, `providers/cohere/rerank.go`, `providers/cohere/rerank_test.go`
- Modify: `ai/registry.go` (RerankingModelProvider + Registry.RerankingModel, mirroring the five existing lookups at ai/registry.go:89-172), `providers/cohere/cohere.go` (constructor)

**Interfaces (Produces):**

`provider/rerank.go`:
```go
// RerankCall is a request to rank Documents by relevance to Query.
type RerankCall struct {
	Query           string
	Documents       []string
	TopN            int // 0 = provider default (all documents)
	ProviderOptions map[string]any
}

// RankedDocument is one scored entry in a rerank response. Index refers to
// the position in RerankCall.Documents.
type RankedDocument struct {
	Index int
	Score float64
}

type RerankResponse struct {
	Results []RankedDocument // sorted most-relevant first, as returned by the provider
	Usage   Usage
	Raw     json.RawMessage
}

type RerankingModel interface {
	Rerank(ctx context.Context, call RerankCall) (*RerankResponse, error)
	ModelID() string
	ProviderName() string
}
```

`ai/rerank.go`:
```go
type RerankOpts struct {
	Model           provider.RerankingModel // required
	Query           string                  // required
	Documents       []string                // required, non-empty
	TopN            int
	MaxRetries      *int
	ProviderOptions map[string]any
	OnRerankStart   func(query string, documents []string) // lifecycle; may be nil
	OnRerankEnd     func(resp *provider.RerankResponse, err error)
}

type RerankResult struct {
	Results []RankedDocument
	Usage   provider.Usage
}

// RankedDocument mirrors provider.RankedDocument plus the resolved document text.
type RankedDocument struct {
	Index    int
	Score    float64
	Document string
}

func Rerank(ctx context.Context, opts RerankOpts) (*RerankResult, error)
```
Validation errors (Model nil, Query empty, Documents empty) → `NewInvalidArgumentError` (same constructor Embed uses). Retries via the same retry helper `Embed` uses (read `ai/embed.go:35-64` and copy its retry/backoff discipline exactly). `OnRerankStart` fires once before the first attempt; `OnRerankEnd` fires once after the final attempt (success or exhausted error). Out-of-range `Index` from a provider → skip that entry (defensive; do not panic on `Documents[i]`).

`ai/registry.go` additions (mirror EmbeddingModel lookup verbatim in structure):
```go
type RerankingModelProvider interface {
	RerankingModel(id string) provider.RerankingModel
}
func (r *Registry) RerankingModel(id string) (provider.RerankingModel, error)
```

`providers/cohere`:
```go
func (p *Provider) RerankingModel(id string) provider.RerankingModel // e.g. "rerank-v3.5"
```
Wire (POST `{baseURL}/rerank`, auth `Authorization: Bearer` — reuse `apiError` from language_model.go and the Call.Headers-can't-clobber-auth discipline is N/A here since RerankCall has no Headers): request `{"model": id, "query": ..., "documents": [...], "top_n": N (omit when 0)}` + ProviderOptions["cohere"] top-level shallow merge last; response `{"results":[{"index":0,"relevance_score":0.9}],"meta":{"billed_units":{"search_units":1}}}` → Results in response order, `Usage{TotalTokens: 0}` (search units are not tokens — leave Usage zero and store the raw body in Raw; document in provider page later).

- [ ] **Step 1: Failing tests** — ai: validation errors; happy path with a mock RerankingModel (results resolved to RankedDocument with Document text, out-of-range index skipped); retry-on-429-then-success; OnRerankStart/End fire once each incl. on final error. cohere: httptest fixture asserting method/path/auth/request shape (top_n omitted when 0, ProviderOptions merge wins), response parsing order preserved, 401/429 → APICallError with correct retryable flag, ctx cancel.
- [ ] **Step 2: Implement provider/rerank.go, ai/rerank.go, registry lookup, cohere rerank until green.**
- [ ] **Step 3: Full check suite (with -race on ./ai/...). Commit** — `feat: reranking — provider.RerankingModel, ai.Rerank, Cohere rerank`

---

### Task 2: Unified Reasoning option with per-provider mapping

**Files:**
- Modify: `provider/call.go` (+field + type), `ai/options.go` (+field on GenerateTextOpts, threaded in buildCall at ai/options.go:235-283), `ai/middleware.go` (DefaultSettingsMiddleware fills Reasoning when unset — follow the TopK/Seed pattern added in wave 9)
- Modify request builders: `internal/openaicompat/wire.go`, `providers/anthropic/wire.go`, `internal/geminicompat/wire.go`, `providers/bedrock/wire.go` (+ language_model.go files where the body is assembled)
- Unsupported providers get an ignore comment: `providers/cohere/wire.go`, `providers/mistral/wire.go`

**Interfaces (Produces):**

`provider/call.go`:
```go
// ReasoningConfig is the unified reasoning/thinking request option.
// Providers map it to their native knob; see each provider's request
// builder for the exact mapping. Effort and BudgetTokens may be set
// together (providers that only understand one use that one; BudgetTokens
// wins where both map to the same knob). ProviderOptions still merge last
// and win on wire-key collision.
type ReasoningConfig struct {
	Effort       string // "", "minimal", "low", "medium", "high" — passed through, not validated
	BudgetTokens *int   // explicit thinking-token budget
}
```
`Call` gains `Reasoning *ReasoningConfig` (place after `Seed`, before `Headers`).

**Effort→budget table** (for providers whose only knob is a token budget; exported as `provider.EffortBudgetTokens(effort string) (int, bool)` in call.go so all providers share one table; unknown effort → false → field omitted):
`minimal→1024, low→4096, medium→8192, high→16384`.

Per-provider mapping (each applied only when `call.Reasoning != nil`):
- **openaicompat** (`internal/openaicompat/wire.go` request struct): `"reasoning_effort": Effort` when Effort ≠ "". BudgetTokens has no OpenAI-wire equivalent → ignored with comment.
- **anthropic** (`providers/anthropic/wire.go`): `"thinking": {"type":"enabled","budget_tokens":N}` where N = `*BudgetTokens` if set, else `EffortBudgetTokens(Effort)`; if neither resolves, omit entirely. Note in the field doc: Anthropic requires `max_tokens > budget_tokens` and temperature restrictions — the SDK passes values through without validating (provider errors surface as APICallError).
- **geminicompat** (`internal/geminicompat/wire.go` generationConfig): `"thinkingConfig": {"thinkingBudget": N, "includeThoughts": true}` with the same N resolution as anthropic; omit when unresolvable.
- **bedrock** (`providers/bedrock/wire.go` Converse body): `"additionalModelRequestFields": {"thinking": {"type":"enabled","budget_tokens":N}}` (same N resolution; merge INTO an existing additionalModelRequestFields map if ProviderOptions already created one — ProviderOptions merge still runs after and wins per key).
- **cohere/mistral**: no reasoning knob → ignored, code comment at the request-builder site.

`GenerateTextOpts` gains `Reasoning *provider.ReasoningConfig` (doc: unified option; per-provider mapping; ProviderOptions win); `buildCall` copies it. `DefaultSettingsMiddleware` fills it when the per-call value is nil (pointer copy is fine — it's read-only by convention).

- [ ] **Step 1: Failing request-shape tests per family** — openaicompat: reasoning_effort present/omitted; anthropic: budget explicit, effort-mapped, neither→omitted; geminicompat: thinkingConfig + includeThoughts; bedrock: additionalModelRequestFields merge with and without pre-existing ProviderOptions map, ProviderOptions key wins; cohere/mistral: absence test (Reasoning set → no new keys). ai: buildCall threads it; DefaultSettingsMiddleware fills/preserves.
- [ ] **Step 2: Implement until green. Full check suite (-race on ./ai/...). Commit** — `feat: unified Reasoning option (effort/budget) mapped per provider`

---

### Task 3: Output modes on GenerateText (text/object/array/choice/json + OutputAs[T])

**Files:**
- Create: `ai/output.go`, `ai/output_test.go`
- Modify: `ai/generate_text.go` (decode after loop; result field), `ai/options.go` (Output field; buildCall integration), `ai/generate_object.go` (export-internally/reuse: `stripFences` and the ResponseFormat/tool-mode branch — extract shared helpers, do NOT duplicate)

**Interfaces (Produces):**

`ai/output.go`:
```go
// Output selects a structured-output mode for GenerateText. Construct one
// with OutputObject, OutputArray, OutputChoice, or OutputJSON; the zero
// value (nil field) means plain text.
type Output interface {
	// schema returns the JSON schema to enforce, or nil for schemaless JSON mode.
	schema() (name string, sch json.RawMessage, err error)
	// decode parses the model's final text into the mode's Go value.
	decode(rawText string) (any, error)
}

func OutputObject[T any]() Output   // schema.For[T]; decodes into T
func OutputArray[T any]() Output    // wraps element schema: {"type":"object","properties":{"elements":{"type":"array","items":<schema.For[T] minus $schema wrapper>}},"required":["elements"],"additionalProperties":false}; decodes the "elements" key into []T
func OutputChoice(choices ...string) Output // {"type":"object","properties":{"result":{"type":"string","enum":[...]}},"required":["result"],"additionalProperties":false}; decodes the "result" key into string
func OutputJSON() Output            // no schema; ResponseFormat json without schema; decodes into any (map[string]any / []any / ...)

// OutputAs extracts the decoded output as T.
func OutputAs[T any](r *GenerateTextResult) (T, error)
```
Implementation notes:
- `schema.For[T]` (internal/schema/schema.go:18) is the derivation entry point; `OutputArray`/`OutputChoice` wrap per the shapes above. Read what `schema.For` emits before wrapping (reuse its raw object output as `items`).
- `GenerateTextOpts` gains `Output Output`. In `buildCall`: when Output non-nil and mode has a schema and `Model.Capabilities().NativeJSON` → `call.ResponseFormat = &provider.ResponseFormat{Type: "json", Schema: sch, Name: name}` (same as buildObjectCall, ai/generate_object.go:81). `OutputJSON` (schemaless) → `ResponseFormat{Type:"json"}` regardless of NativeJSON (providers that can't honor it just return text — decode still applies stripFences).
- When Output has a schema, `!NativeJSON`, and `len(opts.Tools) == 0` → tool-mode fallback exactly as buildObjectCall (single injected ToolDef + forced ToolChoice); the loop then treats that forced tool call's Args as the raw text (no Execute — it is not a real tool; short-circuit before runToolCalls, finish the loop with the args as final text). When `!NativeJSON` AND user tools are present → return `NewInvalidArgumentError("output: model has no native JSON mode and tools are in use; structured output modes require one or the other")` from GenerateText up front.
- Decode: after the loop, `raw := stripFences(result.Text)` (tool-mode path: args JSON directly), `val, err := output.decode(raw)`; decode failure → `*NoObjectGeneratedError{RawText, Cause}` (existing type, ai/generate_object.go). Store `val` in new field `GenerateTextResult.Output any`.
- `OutputAs[T]`: nil r.Output → typed error; type-assert T; for `OutputArray[T]` the stored value is already `[]T` so the assert works; mismatch → descriptive error, not panic.
- StreamText: `Output` is GenerateText-only this wave; StreamText with Output set → `NewInvalidArgumentError` (documented; partial-output streaming is future work — record in migration doc delta table).
- Extract, don't duplicate: `stripFences` stays in generate_object.go and is called from generate_text.go (same package).

- [ ] **Step 1: Failing tests** — OutputObject happy path via mock NativeJSON model; OutputArray decodes []T from elements wrapper (and the schema request shape carries the wrapper); OutputChoice decodes enum + schema shape; OutputJSON decodes arbitrary JSON with no schema in ResponseFormat; tool-mode fallback on !NativeJSON model with no tools (forced tool call args decoded, no tool executed, single step); !NativeJSON+tools → InvalidArgumentError; decode failure → NoObjectGeneratedError with RawText; OutputAs mismatch error; fenced output stripped; multi-step tool loop then Output decode of final text (NativeJSON model + real tools + OutputObject).
- [ ] **Step 2: Implement until green. Full check suite (-race on ./ai/...). Commit** — `feat: structured output modes on GenerateText (object/array/choice/json, OutputAs)`

---

### Task 4: Lifecycle callbacks (model-call + tool-execution + embed events)

**Files:**
- Modify: `ai/options.go` (+4 fields), `ai/generate_text.go` (fire in generate loop + runToolCalls), `ai/stream_text.go` (fire in stream loop), `ai/embed.go` (+2 fields each on EmbedOpts/EmbedManyOpts, fire around embedCall)
- Test: `ai/lifecycle_test.go` (new)

**Interfaces (Produces):**

`ai/options.go` additions to GenerateTextOpts:
```go
// OnModelCallStart fires immediately before each underlying model request
// (once per step, both loops), with the step index (0-based) and the exact
// provider.Call about to be sent.
OnModelCallStart func(stepIndex int, call provider.Call)
// OnModelCallEnd fires when the model request for a step completes.
// In GenerateText, Response is the provider response (nil on error).
// In StreamText, Response is nil; Usage/FinishReason carry what the
// stream's FinishPart reported (zero values if the stream errored first).
// Err is non-nil when the call failed (after retries).
OnModelCallEnd func(end ModelCallEnd)
// OnToolExecutionStart fires before each tool Execute (after approval of
// the call record, before the tool runs). OnToolExecutionEnd fires after,
// with the result record and the raw execution error (nil on success;
// note the loop also reports tool errors through the result record).
OnToolExecutionStart func(stepIndex int, call ToolCallRecord)
OnToolExecutionEnd   func(stepIndex int, result ToolResultRecord, err error)
```
```go
type ModelCallEnd struct {
	StepIndex    int
	Response     *provider.Response // GenerateText only
	Usage        provider.Usage
	FinishReason provider.FinishReason
	Err          error
}
```
`EmbedOpts` and `EmbedManyOpts` each gain:
```go
OnEmbedStart func(values []string)                        // per underlying provider call (per batch in EmbedMany)
OnEmbedEnd   func(resp *provider.EmbeddingResponse, err error) // ditto; resp nil on error
```
Firing rules:
- Callbacks are optional (nil-checked), fired synchronously on the calling goroutine, and must be invoked exactly once per boundary regardless of retries: Start before the FIRST attempt, End after the FINAL attempt (matching where the retry helper wraps the call — fire outside the retry closure).
- StreamText: `OnModelCallStart` before `startStream` per step; `OnModelCallEnd` when the step's FinishPart is observed (Usage/FinishReason from it) or when the stream terminates with an error (Err set, zero Usage). Abandon/ctx-cancel (the OnAbort path): fire OnModelCallEnd with `Err: context.Canceled`? No — keep it simple and truthful: on the abort path OnModelCallEnd does NOT fire (OnAbort already covers it); document this in the field comment.
- runToolCalls: Start/End wrap each `t.Execute` including the RepairToolCall retry path (one Start/End pair per execution attempt is WRONG — one pair per tool call record, around the whole execute-and-maybe-repair sequence).
- EmbedMany fires per batch, in batch order.

- [ ] **Step 1: Failing tests** — GenerateText: Start/End once per step across a 2-step tool loop with exact stepIndex and Response non-nil; End carries Err after retry exhaustion (and Start fired once, not per retry); tool Start/End pair per call with result record; StreamText: Start before parts flow, End with FinishPart usage, End-with-Err on stream error, no End on abandon (OnAbort fires instead); Embed/EmbedMany: per-batch pairs in order, End with err on failure. Race test: callbacks touching a shared slice under -race in StreamText.
- [ ] **Step 2: Implement until green. Full check suite (-race on ./ai/...). Commit** — `feat: lifecycle callbacks — model-call, tool-execution, embed events`

---

### Task 5: Wave-10 docs + CHANGELOG

**Files:**
- Modify: `docs/core/generating-text.md` (Output modes section w/ compile-verified snippets incl. OutputAs; lifecycle callbacks), `docs/core/structured-output.md` (cross-link: GenerateObject vs Output modes — when to use which), `docs/core/reasoning.md` (unified Reasoning option, per-provider mapping table incl. the effort→budget table, ProviderOptions-win precedence), `docs/core/embeddings.md` (embed events + NEW Reranking section: ai.Rerank, RerankOpts, registry), `docs/core/middleware-and-registry.md` (RerankingModel registry row; DefaultSettings fills Reasoning), `docs/core/provider-options.md` (Reasoning precedence note), `docs/providers/cohere.md` (rerank endpoint, model ids, usage note re search_units, live-testing note), `docs/providers/README.md` (rerank column/matrix row), `README.md` (feature list + matrix), `docs/migrating-from-vercel-ai-sdk.md` (move Output modes/rerank/reasoning/lifecycle from "Planned" to "Shipped"; note StreamText partial-output streaming still planned), `CHANGELOG.md` (Unreleased entries), `docs/README.md` (only if a new page were added — none is; verify links).
- Same verification discipline as prior waves: every snippet compile-verified in a scratch main, claims grepped against code, matrix cell counts checked, links resolved.

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 10 — output modes, rerank, reasoning, lifecycle callbacks`

---

## Self-Review Notes

- Roadmap deviation, recorded: W10 ships Cohere rerank only; Voyage/Mixedbread arrive with their providers in W13 (the roadmap lists them in both waves — W13 governs, since the providers don't exist yet). Roadmap's "precedence over ProviderOptions" for Reasoning is overridden by the repo-wide "ProviderOptions merge last and win" convention — documented in Task 2 and the docs task.
- v6-delta ledger for THIS wave: Output text/object/array/choice/json ✓ (GenerateText only; StreamText partial-output streaming deferred to a later wave — typed error today, noted in migration doc), OutputAs[T] ✓, rerank() ✓ (Cohere), unified reasoning ✓ (openai effort, anthropic/gemini/bedrock budget w/ shared effort table, others documented no-op), lifecycle events ✓ (model-call, tool-execution, embed, rerank via OnRerankStart/End in Task 1).
- Task 3 is the risky one (loop integration): the !NativeJSON tool-mode short-circuit must not run Execute and must produce exactly one step; the +tools error is up-front, not mid-loop.
- Task 4's "no OnModelCallEnd on abort" ruling keeps callback semantics disjoint from OnAbort — reviewer should check the wave-9 OnAbort test file for interference.
