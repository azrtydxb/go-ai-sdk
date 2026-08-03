# Agents

`package agent` is a thin, reusable wrapper over `ai.GenerateText`/
`ai.StreamText`: an `Agent` bundles a model, system instructions, tools, and
a handful of loop-shaping options into one value that can be run repeatedly
with different inputs via `RunOpts`, and exposed to another agent as a tool
via `AsTool`. It's the `go-ai-sdk` equivalent of the AI SDK's
`ToolLoopAgent` — see the [naming note](#naming-toolloopagent-vs-agent)
below.

`Agent` contains **no loop logic of its own.** `Generate`/`Stream` assemble
an `ai.GenerateTextOpts` from the `Agent`'s fields and the given `RunOpts`,
apply `PrepareOpts` last, and delegate entirely to `ai.GenerateText`/
`ai.StreamText`. Every tool-calling, retry, streaming, and approval
semantic documented on those functions applies unchanged — this page only
covers what `Agent` adds on top.

## When to use Agent vs raw GenerateText

Reach for `ai.GenerateText`/`ai.StreamText` directly for a one-off call
whose options don't need to be reused. Reach for `Agent` when the same
model+instructions+tools configuration will run more than once — a chat
loop, a sub-agent delegated to via `AsTool`, or anywhere the call site
shouldn't have to re-specify `Tools`/`Instructions`/`MaxSteps` every time.

## Agent fields

```go
weatherAgent := &agent.Agent{
	Model:        model,
	Instructions: "You are a helpful weather assistant.",
	Tools:        []ai.Tool{weatherTool},
	MaxSteps:     5,
}
```

- **`Model`** (`provider.LanguageModel`, required) — passed through to
  `GenerateTextOpts.Model`.
- **`Instructions`** (`string`) — passed through as `GenerateTextOpts.System`.
- **`Tools`** (`[]ai.Tool`) — offered to the model on every run.
- **`MaxSteps`** (`int`) — caps the tool-calling loop. Zero means the
  Agent's own default of **8**, not `ai.GenerateTextOpts`'s default — see
  [MaxSteps default](#maxsteps-default-8-vs-ais-1) below.
- **`StopWhen`** (`func([]ai.Step) bool`) — passed through unchanged to
  `GenerateTextOpts.StopWhen`.
- **`Output`** (`ai.Output`) — passed through unchanged; selects a
  structured-output mode for `Generate` (see [Stream and Output](#stream-and-output-erroutputwithstreamtext) below for `Stream`'s restriction).
- **`RuntimeContext`** (`ai.RuntimeContext`) — passed through unchanged; see
  [RuntimeContext and sub-agent scoping](#runtimecontext-and-sub-agent-scoping)
  below for how this interacts with `AsTool`.
- **`ApproveToolCall`** (`func(context.Context, ai.ApprovalRequest) (ai.ApprovalDecision, bool)`)
  — passed through unchanged to `GenerateTextOpts.ApproveToolCall`; see
  [Approval passthrough](#approval-passthrough) below.
- **`PrepareOpts`** (`func(opts *ai.GenerateTextOpts)`) — see
  [PrepareOpts runs last](#prepareopts-runs-last) below.

## RunOpts

Each call to `Generate`/`Stream` takes a `RunOpts`:

```go
type RunOpts struct {
	Prompt    string
	Messages  []provider.Message
	Approvals []ai.ApprovalDecision
}
```

Exactly one of `Prompt`/`Messages` must be set — `Agent` does not
re-validate this itself; it's delegated entirely to
`ai.GenerateText`/`ai.StreamText`'s own `buildCall` check (same error,
`ai: exactly one of Prompt or Messages must be set`, or `ai: GenerateTextOpts.Model is required`
for a nil `Model`). `Approvals` is passed through unchanged to
`GenerateTextOpts.Approvals` — see
[Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
for the full resume flow this feeds into.

## Generate and Stream

```go
result, err := weatherAgent.Generate(ctx, agent.RunOpts{
	Prompt: "What's the weather in Ghent?",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Text)
```

`Generate` returns `(*ai.GenerateTextResult, error)`; `Stream` returns
`(*ai.TextStream, error)` — the real return types of `ai.GenerateText`/
`ai.StreamText`, not `Agent`-specific wrappers. A `nil` `*Agent` receiver
returns a plain error from both (`agent: nil Agent`) rather than panicking;
every other validation (nil `Model`, `Prompt`/`Messages` both-or-neither) is
left to the underlying `ai.GenerateText`/`ai.StreamText` call.

### MaxSteps default: 8 vs ai's 1

`ai.GenerateTextOpts.MaxSteps` defaults to **1** (a single model call, no
tool loop) when left unset — or **16** if `StopWhen` is also set. `Agent`
is meant to run a multi-step tool-calling loop by default, so its own
default (applied when `Agent.MaxSteps` is `0`) is **8** instead — a
deliberate divergence, documented on both the package doc comment and the
`MaxSteps` field. Set `Agent.MaxSteps` explicitly to override it either way.

### Stream and Output: ErrOutputWithStreamText

If `Agent.Output` is set, `Stream` does not intercept or special-case
`ai.StreamText`'s own restriction: `ai.StreamText` returns
`ai.ErrOutputWithStreamText` immediately rather than streaming, and that
same error passes straight through `Agent.Stream` to the caller — it is
never silently dropped or converted into an empty stream.

## PrepareOpts runs last

`PrepareOpts`, when set, receives the fully-assembled `ai.GenerateTextOpts`
— every other `Agent` field already applied, plus the run's
`Prompt`/`Messages`/`Approvals` — right before the call. It runs **last**,
so whatever it sets on `opts` wins over every field above:

```go
a := &agent.Agent{
	Model: model,
	Tools: []ai.Tool{weatherTool},
	PrepareOpts: func(opts *ai.GenerateTextOpts) {
		// Overrides Agent.MaxSteps (or the default of 8) for this run.
		opts.MaxSteps = 20
	},
}
```

## Approval passthrough

`Agent.ApproveToolCall` and `RunOpts.Approvals` are threaded straight
through to `GenerateTextOpts.ApproveToolCall`/`.Approvals` — an `Agent` run
suspends and resumes exactly like a raw `ai.GenerateText`/`ai.StreamText`
call with the same options set directly. See
[Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
for the full mechanics (decision order, batch atomicity, denial shape,
suspension/resume).

## RuntimeContext and sub-agent scoping

`Agent.RuntimeContext` is passed through to `GenerateTextOpts.RuntimeContext`
unchanged — see [Tools § RuntimeContext](tools.md#runtimecontext) for the
base mechanism. The scoping rule that matters specifically for agents:
`ai.RuntimeContext` is a no-op when `nil` (it never clears an
already-installed value on `ctx`), so an `Agent` with `RuntimeContext` left
unset **inherits** whatever `RuntimeContext` was already on the `ctx` it's
called with — including, when run via `AsTool` as a sub-agent, the
**parent** agent's `RuntimeContext` (already installed on `ctx` by the time
the parent's tool loop calls the sub-agent tool's `Execute`). Setting
`Agent.RuntimeContext` to any non-nil value — including an explicitly empty
`ai.RuntimeContext{}` — overrides that inheritance and installs/shadows it
for the sub-agent's own run instead. An explicitly empty
`ai.RuntimeContext{}` is therefore the way to isolate a sub-agent's tools
from a parent's `RuntimeContext` rather than merely leaving the field
unset.

## AsTool: exposing an Agent as a tool

`agent.AsTool(a *Agent, name, description string) ai.Tool` wraps `a` as an
`ai.Tool` so a parent agent (or a raw `ai.GenerateText`/`ai.StreamText`
call) can delegate a sub-task to it:

```go
researchAgent := &agent.Agent{
	Model:        model,
	Instructions: "You research topics and report findings concisely.",
	Tools:        []ai.Tool{searchTool},
}

researchTool := agent.AsTool(researchAgent, "research", "Delegates a research task to a specialized sub-agent.")

parent := &agent.Agent{
	Model: model,
	Tools: []ai.Tool{researchTool},
}

result, err := parent.Generate(ctx, agent.RunOpts{
	Prompt: "Research the history of the Go programming language, then summarize it.",
})
```

### Schema: `{"task"}`

The returned tool's schema always takes a single required string field:

```json
{"type":"object","properties":{"task":{"type":"string","description":"The task for the research agent."}},"required":["task"],"additionalProperties":false}
```

(with `<name>` — the `name` argument passed to `AsTool` — interpolated into
the description). This is a hand-written schema, not derived by reflection
— `AsTool` always takes exactly `{"task": string}`, regardless of what the
sub-agent's own tools look like.

### Execute: Output-else-Text

When called, the tool runs `a.Generate(ctx, agent.RunOpts{Prompt: task})`
and returns:

- `result.Output`, when the sub-agent's `Output` field is set and produced
  a decoded value;
- `result.Text` otherwise.

### Malformed args: wrapped in `*ai.InvalidToolArgumentsError`

Malformed `{"task": ...}` args passed to `Execute` are wrapped in
`*ai.InvalidToolArgumentsError{ToolName: name, Args, Cause}` — the same
typed error every other `ai.Tool` produces for bad args (see
[Tools § Execution error taxonomy](tools.md#execution-error-taxonomy)), so
`errors.As` works and `GenerateTextOpts.RepairToolCall`'s bad-args repair
path is offered a chance to fix it, exactly as it would be for a
hand-written tool.

### Errors: wrapped in `*ai.ToolExecutionError`

A sub-agent error from `Generate` is wrapped in a
`*ai.ToolExecutionError{ToolName: name, Cause: err}` before `Execute`
returns it — the same error taxonomy `ai.NewTool`-built tools already
produce for a failing handler, so a failing sub-agent looks like any other
failing tool to the parent's loop and to code that type-switches or
`errors.As`es on `ToolResultRecord.Err`:

```go
res, err := parent.Generate(ctx, agent.RunOpts{Prompt: "..."})
// If the sub-agent's Generate failed, res.Steps[...].ToolResults[...].Err
// is a *ai.ToolExecutionError whose Cause is the sub-agent's real error —
// errors.Is/errors.As still reach it through Cause/Unwrap.
```

### Suspension: sub-agents must decide approvals inline

**A sub-agent run via `AsTool` cannot suspend and resume the way a top-level
`Agent`/`ai.GenerateText` call can.** If the sub-agent's own `Generate` call
returns a result whose `PendingApprovals` is non-empty (one of the
sub-agent's tools needed approval and no decision was available for it),
`Execute` does not return `""` or any other value as if that were a
successful result — it returns

```go
&ai.ToolExecutionError{ToolName: name, Cause: agent.ErrSubAgentSuspended}
```

Check for this with `errors.Is(err, agent.ErrSubAgentSuspended)`.

The reason there's no resume channel: resuming a suspended run means
resending `Messages` (ending in the unanswered tool-call batch) with
`Approvals` set — but the parent's tool loop only sees the sub-agent tool's
single `Execute` call and its returned `(any, error)`; it has no way to
receive the sub-agent's `PendingApprovals`/`Messages` out through that
return value, stash them, and later feed `Approvals` back into a *second*
call to the same sub-agent run. By the time `Execute` returns, that
suspended conversation state is gone.

**The fix is to decide approvals inline, before they ever reach this
boundary:** set `Agent.ApproveToolCall` on the sub-agent so every
approval-needing call it makes is resolved synchronously (see
[Approval passthrough](#approval-passthrough) above and
[Tools § Inline flow](tools.md#inline-flow-approvetoolcall-decides-synchronously)).
A sub-agent run purely through `AsTool` should never be *expected* to
suspend — if one of its tools needs a human-in-the-loop decision, wire that
decision through `ApproveToolCall`, or don't delegate that tool through a
sub-agent at all.

## Naming: ToolLoopAgent vs Agent

The Vercel AI SDK's equivalent construct is documented as `ToolLoopAgent`.
`go-ai-sdk` names it plainly `Agent` — there's no other "agent" concept in
this SDK for it to disambiguate from, so the shorter name was chosen
instead of porting `ToolLoopAgent` verbatim. See
[Migrating from the Vercel AI SDK](../migrating-from-vercel-ai-sdk.md) for
the full API mapping.

## Source of truth

- [`agent/agent.go`](../../agent/agent.go) (`Agent`, `RunOpts`, `Generate`,
  `Stream`)
- [`agent/tool.go`](../../agent/tool.go) (`AsTool`)
- [`ai/options.go`](../../ai/options.go), [`ai/generate_text.go`](../../ai/generate_text.go),
  [`ai/stream_text.go`](../../ai/stream_text.go) — everything `Agent`
  delegates to

See also: [Tools](tools.md) for the underlying tool-calling contract and
approvals; [Generating text](generating-text.md) for the full
`GenerateTextOpts` reference.
