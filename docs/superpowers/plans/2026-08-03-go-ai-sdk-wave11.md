# go-ai-sdk Wave 11 Implementation Plan (agents, approvals, RuntimeContext, Sandbox/Code Mode)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** AI SDK 6 agent-layer parity: an `agent` package (ToolLoopAgent equivalent + agent-as-tool), tool-execution approvals with a resumable pending-approval flow, a RuntimeContext bag for tools, and Code Mode driven by a caller-supplied `Sandbox` interface — plus the four carried wave-10 minors.

**Architecture:** Approvals and RuntimeContext land inside the existing tool loop (`runToolCalls` + both loops' entry paths); the resumable flow reuses the SDK's round-trippable `Messages` transcript (a resume is just a call whose Messages end in an unanswered assistant tool-call batch). `agent` and `codemode` are new top-level packages layered purely on `ai` (no provider knowledge). Code Mode ships as prompting + orchestration around a user-implemented `Sandbox`, per the standing scope ruling.

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies; ADDITIVE only on existing exported surfaces.
- Tool-loop invariants preserved: batch atomicity on unknown-tool (NoSuchToolError aborts batch), RepairToolCall semantics unchanged, one RoleTool message per batch with ToolResultPart per call, error results as `IsError: true` string results on the wire.
- Approvals must work identically in GenerateText and StreamText (same `runToolCalls`-adjacent code path).
- `provider/providertest` untouched. Providers untouched except Task 1's `resolveBudgetTokens` consolidation.
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` + `go test -race ./ai/... ./agent/... ./codemode/...` (as packages exist).
- Commits conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Wave-10 carryovers (mechanical)

**Files:**
- Modify: `provider/call.go` (+`ResolveBudgetTokens`), `providers/anthropic/wire.go`, `internal/geminicompat/wire.go`, `providers/bedrock/wire.go` (each: delete the private `resolveBudgetTokens`, call the shared one), `ai/options.go` (+`SchemaDescription` field + PrepareStep doc sentence), `ai/generate_text.go` (use SchemaDescription; multi-call doc note), `docs/core/generating-text.md` (SchemaDescription + the two doc notes)

**Exact changes:**
1. `provider/call.go`: add
```go
// ResolveBudgetTokens resolves a ReasoningConfig to a concrete thinking-token
// budget: BudgetTokens when set, else the EffortBudgetTokens mapping of
// Effort. ok is false when neither resolves.
func ResolveBudgetTokens(cfg *ReasoningConfig) (n int, ok bool) {
	if cfg == nil {
		return 0, false
	}
	if cfg.BudgetTokens != nil {
		return *cfg.BudgetTokens, true
	}
	return EffortBudgetTokens(cfg.Effort)
}
```
Replace the three identical private helpers (`providers/anthropic/wire.go:762-767`, `internal/geminicompat/wire.go:295-300`, `providers/bedrock/wire.go:1042-1047`) with calls to it. Existing wire tests must pass unchanged.
2. `ai/options.go`: add `SchemaDescription string` to `GenerateTextOpts` (doc: describes the expected output schema; used as the injected output tool's Description in the tool-mode fallback and passed as ResponseFormat description where providers support one — check `provider.ResponseFormat` for a Description field; if none exists, tool-mode Description only). Thread it in `ai/generate_text.go`'s fallback ToolDef.
3. `PrepareStep` doc: add the sentence "A model swapped in via PrepareStep is not re-checked against Output's NativeJSON capability requirement — the output strategy is fixed from opts.Model at entry; swapping to a model without native JSON mid-loop leaves the schema unenforced on that provider."
4. `Output` field doc + docs: note that in the tool-mode fallback, if the model emits the output tool more than once in one response, only the first matching call is decoded and answered.
5. Test: SchemaDescription reaches the injected ToolDef (extend the existing tool-mode fallback test); `ResolveBudgetTokens` unit test in provider.

- [ ] **Step 1: Implement + tests green. Full check suite. Commit** — `refactor: wave-10 carryovers — shared ResolveBudgetTokens, SchemaDescription, doc notes`

---

### Task 2: RuntimeContext + tool approvals with resumable flow

**Files:**
- Create: `ai/approval.go`, `ai/approval_test.go`, `ai/runtime_context.go`, `ai/runtime_context_test.go`
- Modify: `ai/options.go`, `ai/generate_text.go`, `ai/stream_text.go`, `ai/errors.go`

**Interfaces (Produces):**

`ai/runtime_context.go`:
```go
// RuntimeContext is an arbitrary bag of application values made available
// to tools during execution via RuntimeContextFrom.
type RuntimeContext map[string]any

// RuntimeContextFrom returns the RuntimeContext installed for this tool
// loop, or nil when none was configured.
func RuntimeContextFrom(ctx context.Context) RuntimeContext
```
`GenerateTextOpts` gains `RuntimeContext RuntimeContext`; both loops install it on the ctx passed to `Tool.Execute` (unexported context key; installed once, before the loop). `Embed`/`Rerank` are NOT touched (tools only).

`ai/approval.go`:
```go
// ApprovalRequirer is implemented by Tools whose calls need approval
// before execution. RequireApproval wraps any Tool to add it.
type ApprovalRequirer interface {
	ApprovalRequired(ctx context.Context, args json.RawMessage) bool
}

// RequireApproval wraps t so every call requires approval; with a
// non-nil when func, only calls for which when returns true do.
func RequireApproval(t Tool, when ...func(ctx context.Context, args json.RawMessage) bool) Tool

type ApprovalRequest struct {
	StepIndex int
	Call      ToolCallRecord
}

type ApprovalDecision struct {
	ToolCallID string
	Approved   bool
	Reason     string // included in the denial tool result sent to the model
}
```
`GenerateTextOpts` gains:
```go
// ApproveToolCall decides approval-needing calls inline. Return
// (decision, true) to decide; (zero, false) to leave the call pending,
// which suspends the loop (see PendingApprovals).
ApproveToolCall func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, bool)
// Approvals supplies out-of-band decisions on a resume call, matched by
// ToolCallID against the unanswered assistant tool-call batch at the end
// of Messages.
Approvals []ApprovalDecision
```
`GenerateTextResult` gains `PendingApprovals []ApprovalRequest`.
`ai/errors.go` gains:
```go
type ToolApprovalDeniedError struct {
	ToolName string
	Reason   string
}
func (e *ToolApprovalDeniedError) Error() string // "ai: tool "name" execution denied: reason" (reason omitted when empty)
```

**Semantics (exact rules):**
1. Decision resolution for each call whose tool needs approval (`ApprovalRequirer` check, args passed): first match in `opts.Approvals` by ToolCallID; else `ApproveToolCall` if set and it returns ok=true; else PENDING.
2. Batch atomicity: if ANY call in the step's batch is PENDING, NO tool in that batch executes. The loop stops: result has `PendingApprovals` (one entry per approval-needing call without a decision, in call order), `FinishReason` = the step's real finish reason (`tool-calls`), `Messages` ending with the assistant tool-call message (round-trippable), Steps includes the final (tool-result-less) step. Not an error. `OnFinish` fires normally. OnToolExecutionStart/End do NOT fire for the unexecuted batch.
3. Denied calls execute nothing: `ToolResultRecord{Err: &ToolApprovalDeniedError{...}}`, serialized to the model like any tool error (`IsError: true`, text = the error string). OnToolExecutionStart/End DO fire for denied calls (End with the denial error) — they are part of the executed batch.
4. Resume: when `Messages` is set and its last message is an assistant message containing ToolCallParts with no subsequent RoleTool message, both loops first run that batch (approval rules applied with the incoming `Approvals`) before the first model call; the RoleTool results message is appended and the loop proceeds normally. Repeated suspension (still-pending calls on resume) is allowed and returns PendingApprovals again. This resume path also composes with tools that need no approval (they simply execute).
5. StreamText: identical rules; on suspension the stream emits its parts as usual then finishes; `PendingApprovals` is delivered via the StreamText result surface that carries GenerateTextResult (OnFinish and/or the stream's result accessor — match how StreamText exposes the final result today and put PendingApprovals there). The suspended batch fires no tool-execution events.
6. Approval checks happen AFTER unknown-tool validation and BEFORE any execution or repair.
7. `RuntimeContext` is installed for `ApprovalRequired` and `ApproveToolCall` invocations too (they receive the same ctx tools see).

**Tests (ai/approval_test.go + runtime_context_test.go, GenerateText AND StreamText variants):** RequireApproval static + conditional `when`; inline ApproveToolCall approve → executes; deny → ToolApprovalDeniedError result with Reason on the wire (assert RoleTool message text) and model sees IsError; pending → suspension with correct PendingApprovals/Messages/FinishReason and no Execute call (instrument the tool); resume with Approvals executes and continues to a second model step; resume with a denial; repeated suspension; mixed batch (approval + plain tool) suspends everything; batch with decisions for all executes all; RuntimeContextFrom returns values inside Execute and inside ApprovalRequired/ApproveToolCall, nil when unset; lifecycle events per rule 2/3; race test on the StreamText suspension path.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on ai). Commit** — `feat: tool-execution approvals with resumable flow + RuntimeContext`

---

### Task 3: agent package

**Files:**
- Create: `agent/agent.go`, `agent/agent_test.go`, `agent/tool.go`, `agent/tool_test.go`

**Interfaces (Produces):**
```go
package agent

// Agent is a reusable configuration for running a model+tools loop —
// the AI SDK ToolLoopAgent equivalent.
type Agent struct {
	Model        provider.LanguageModel // required
	Instructions string                 // system prompt
	Tools        []ai.Tool
	MaxSteps     int          // default 8 when 0 (document: agents default to multi-step, unlike raw GenerateText's default 1... check ai's actual default and document the difference)
	StopWhen     func([]ai.Step) bool
	Output       ai.Output
	RuntimeContext  ai.RuntimeContext
	ApproveToolCall func(ctx context.Context, req ai.ApprovalRequest) (ai.ApprovalDecision, bool)
	// PrepareOpts, when set, receives the fully-assembled GenerateTextOpts
	// before each run for arbitrary customization (settings, callbacks,
	// ProviderOptions). Runs last; whatever it sets wins.
	PrepareOpts func(opts *ai.GenerateTextOpts)
}

// RunOpts is one invocation of an Agent. Exactly one of Prompt/Messages.
type RunOpts struct {
	Prompt    string
	Messages  []provider.Message
	Approvals []ai.ApprovalDecision // resume support, passed through
}

func (a *Agent) Generate(ctx context.Context, run RunOpts) (*ai.GenerateTextResult, error)
func (a *Agent) Stream(ctx context.Context, run RunOpts) (<same type ai.StreamText returns>, error)
```
Both assemble a `GenerateTextOpts` (Model, System=Instructions, Tools, MaxSteps default, StopWhen, Output, RuntimeContext, ApproveToolCall, Prompt/Messages/Approvals from run), apply `PrepareOpts`, and delegate to `ai.GenerateText`/`ai.StreamText`. Zero loop logic of its own. Validation (nil Model, both/neither Prompt+Messages) delegates to ai's existing errors — do not duplicate.

`agent/tool.go`:
```go
// AsTool exposes an Agent as an ai.Tool so a parent agent can delegate to
// it. The tool takes {"task": string} and returns the sub-agent's final
// text (or its decoded Output when the agent has one).
func AsTool(a *Agent, name, description string) ai.Tool
```
Schema: hand-written `{"type":"object","properties":{"task":{"type":"string","description":"The task for the <name> agent."}},"required":["task"],"additionalProperties":false}`. Execute: `a.Generate(ctx, RunOpts{Prompt: task})`; error propagates (loop wraps it in ToolExecutionError); returns `result.Output` when non-nil else `result.Text`. Parent RuntimeContext flows via ctx automatically ONLY if the sub-agent doesn't overwrite it — sub-agent installs its own RuntimeContext; document that the sub-agent's own RuntimeContext (possibly nil) governs its tools.

**Tests:** Generate assembles opts correctly (assert via MockModel's recorded Call: system message, tools present, max steps honored by scripting a long loop); default MaxSteps applied; PrepareOpts runs last and can override; Stream smoke (mock stream); AsTool executes a scripted sub-agent loop and returns final text; AsTool with Output returns decoded value; sub-agent error propagates; approval passthrough (agent-level ApproveToolCall denies → denial visible in transcript); RunOpts validation (both prompt+messages → error from ai layer).

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: agent package — Agent, RunOpts, AsTool`

---

### Task 4: codemode package (Sandbox + Code Mode)

**Files:**
- Create: `codemode/codemode.go`, `codemode/codemode_test.go`, `codemode/apidoc.go`, `codemode/apidoc_test.go`

**Interfaces (Produces):**
```go
package codemode

// Sandbox executes model-written code. The SDK ships no runtime — the
// caller implements this against their own sandbox (subprocess, container,
// embedded interpreter). Execute must honor ctx cancellation.
type Sandbox interface {
	Execute(ctx context.Context, code string, env Env) (*Result, error)
}

// Env is the binding surface a Sandbox exposes to running code.
type Env struct {
	// CallTool dispatches a tool invocation from sandboxed code to the
	// underlying ai.Tools by name. Args is the raw JSON argument object.
	CallTool func(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// Result is what a Sandbox returns to the model.
type Result struct {
	Output string // printed/returned output, sent back to the model verbatim
	Logs   []string
}

// Language configures the language named in the generated tool
// description and API docs (purely prompting — the Sandbox defines what
// actually runs). Default "javascript".
type Options struct {
	Language    string
	MaxOutputBytes int // truncate Result.Output sent to the model; 0 = 16384
}

// Tool wraps tools into a single "run_code" ai.Tool: the model writes
// code that calls the tools through the sandbox's binding, instead of
// invoking them one call at a time.
func Tool(sandbox Sandbox, tools []ai.Tool, opts *Options) ai.Tool
```
Behavior of the returned Tool:
- Name `run_code`; Description = fixed preamble ("Execute <language> code in a sandbox. The following functions are available to your code:") + the generated API docs (below) + usage rules (call functions with a single object argument matching the schema; return or print your final answer).
- Schema: `{"type":"object","properties":{"code":{"type":"string","description":"The <language> code to execute."}},"required":["code"],"additionalProperties":false}`.
- Execute: decode `{"code": string}`, build `Env{CallTool: dispatcher}` where the dispatcher resolves by exact tool name (unknown name → error return to the sandbox, NOT a Go panic; the error text lists available tool names), invokes `sandbox.Execute(ctx, code, env)`, truncates `Result.Output` to MaxOutputBytes with a `"\n[truncated]"` suffix when exceeded, appends Logs as `"\nlog: <line>"` lines, returns the string. Sandbox error → plain error (loop wraps as ToolExecutionError). ctx passthrough everywhere; RuntimeContextFrom works inside dispatched tools because ctx flows through CallTool.

`codemode/apidoc.go`:
```go
// APIDoc renders tool signatures as language-flavored API documentation
// for the run_code tool description: one entry per tool with name,
// description, and its JSON schema rendered as a parameter listing.
func APIDoc(language string, tools []ai.Tool) string
```
Rendering: per tool, `functionName(args: {field: type, ...}) — description`, fields read from the schema JSON (`properties`, `required` — optional fields suffixed `?`; nested objects rendered inline one level deep, deeper as `object`). MCP tools' opaque schemas: render whatever properties exist; a schema without `properties` renders as `(args: object)`. Deterministic output (sorted only where the schema itself has no order — Go maps: sort property names alphabetically).

**Tests:** APIDoc golden output for two tools incl. optional/nested/array fields and a properties-less schema; Tool description contains the API doc + language; dispatcher routes to the right tool and returns its result; unknown tool name → error listing available names; sandbox receives the exact code + working CallTool (fake Sandbox that calls back into a recording tool); output truncation at MaxOutputBytes; Logs appended; sandbox error surfaces as Tool error; end-to-end through ai.GenerateText with a MockModel scripting a run_code call (assert the tool result text the model would see); RuntimeContext visible inside a dispatched tool.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: codemode package — Sandbox interface and code-mode tool`

---

### Task 5: Wave-11 docs + CHANGELOG

**Files:**
- Create: `docs/core/agents.md` (Agent, RunOpts, AsTool, defaults, approval passthrough, when-to-use vs raw GenerateText), `docs/core/code-mode.md` (Sandbox contract — what the caller must implement, security responsibilities live with the sandbox implementer; Env/CallTool; APIDoc; full worked example with a fake sandbox)
- Modify: `docs/core/tools.md` (approvals section: RequireApproval, ApprovalRequirer, inline vs resumable flow with a full resume example; RuntimeContext section; ToolApprovalDeniedError in the error taxonomy), `docs/core/generating-text.md` (Approvals/PendingApprovals/resume on the opts/result reference; SchemaDescription), `docs/core/streaming.md` (suspension behavior in streams), `docs/core/errors-and-retries.md` (ToolApprovalDeniedError row), `docs/README.md` (+2 pages), `README.md` (feature list), `docs/migrating-from-vercel-ai-sdk.md` (agents/approvals/Code Mode rows → Shipped; add RuntimeContext row as Shipped; note ToolLoopAgent naming), `CHANGELOG.md` (Unreleased wave-11 entries), `docs/core/reasoning.md` (only if ResolveBudgetTokens is worth a mention — mapping unchanged, likely no edit; verify)
- Verification discipline as prior waves: snippets compile-verified, claims grepped, links resolve, matrices consistent.

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 11 — agents, approvals, RuntimeContext, code mode`

---

## Self-Review Notes

- Approval design deviations from Vercel worth flagging in the migration doc: Vercel models pending approvals as special message parts in the UI stream; we model them as a suspended result + `Approvals` on the resume call (no UI layer). Vercel's `needsApproval` lives on the tool definition; ours is `RequireApproval`/`ApprovalRequirer` (interface-based, works for MCP tools too by wrapping).
- Task 2's resume-detection (unanswered assistant tool-call batch at the end of Messages) is new loop-entry behavior even without approvals — it makes any tool-calls-terminated transcript resumable. Document it in generating-text.md as its own capability.
- Task 3 must check ai's real MaxSteps default before documenting the agent default (plan says raw default is 1? verify `MaxSteps == 0` handling in generate_text.go and state the agent's default 8 relative to it).
- Task 4's codemode.Tool is deliberately provider-agnostic prompting; no sandbox implementation ships — the fake Sandbox in tests is the reference for implementers and appears in code-mode.md.
- Order: 1 → 2 → 3 → 4 → 5 (3 depends on 2's exported types; 4 only on ai.Tool + RuntimeContext).
