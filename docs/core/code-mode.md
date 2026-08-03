# Code Mode

"Code Mode" is an alternative to exposing each tool as a separate function
call the model invokes one at a time: instead, `package codemode` wraps a
set of `ai.Tool`s into a single `run_code` tool. The model is shown an API
doc of the available functions and writes a short program that calls them,
and that program runs inside a sandbox **the caller supplies** — this can
reduce round trips for multi-tool-call tasks (loops, conditionals, and
intermediate values all happen in one sandboxed program instead of one
model turn per tool call).

**The SDK ships no runtime.** `codemode` never executes model-written code
itself: it renders the API doc, decodes the `run_code` arguments, dispatches
`CallTool` invocations from the sandbox back to the underlying `ai.Tool`s by
name, and post-processes the sandbox's `Result` for the model. Every
security guarantee the resulting tool appears to have — resource limits,
network access, filesystem access, wall-clock limits, process isolation —
is exactly what the caller's `Sandbox.Execute` implementation provides.
**Security is the sandbox implementer's responsibility, not `codemode`'s.**

## The Sandbox contract

```go
type Sandbox interface {
	Execute(ctx context.Context, code string, env Env) (*Result, error)
}

type Env struct {
	CallTool func(ctx context.Context, name string, args json.RawMessage) (any, error)
}

type Result struct {
	Output string // printed/returned output, sent back to the model verbatim
	Logs   []string
}
```

Implement `Sandbox` against whatever you trust to run model-written code —
a subprocess, a container, an embedded interpreter (e.g. goja for
JavaScript, a WASM runtime). `Execute` must honor `ctx` cancellation.
`Env.CallTool` is the only bridge back from the sandbox into the wrapped
`ai.Tool`s: whatever binding the sandbox exposes to the running code (a
global function, an imported module, an RPC call) should ultimately invoke
`env.CallTool(ctx, name, args)` for each tool call the model's code makes.

`Result.Output` is what the model sees as the tool's answer; `Result.Logs`
is rendered separately (see [Output post-processing](#output-post-processing-truncation-and-logs)
below) — use `Logs` for incidental print/debug output the sandboxed code
produced along the way, and `Output` for the actual answer.

`ctx` flows through `CallTool` unchanged into each dispatched tool's
`Execute`, so `ai.RuntimeContextFrom(ctx)` works exactly the same inside a
tool called through Code Mode as it does when that tool is called directly
(no `run_code` indirection) — see
[Tools § RuntimeContext](tools.md#runtimecontext).

## Security: approvals are checked and refused, never suspended

A tool wrapped in `ai.RequireApproval` (see
[Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution))
is checked on every dispatch from sandboxed code: if
`ApprovalRequired(ctx, args)` reports `true`, `dispatch` refuses the call
with an error —

```
codemode: tool "delete_record" requires approval and cannot be called from code mode
```

— instead of executing it. The tool's `Execute` never runs for a refused
call, the same way a denied call's `Execute` never runs in the ordinary tool
loop (see [The IsError result convention](tools.md#the-iserror-result-convention)).

**There is no suspension channel from inside a sandbox.** The ordinary
`GenerateText`/`StreamText` tool loop can suspend a whole batch pending
approval because it hands the caller a resumable
[`PendingApprovals`](tools.md#suspension-result-shape) result and stops —
but a sandboxed program has already been handed control and has no
equivalent way to pause mid-execution and hand back a decision point.
Refusing outright is the only option that doesn't silently bypass the
approval gate.

Consequences for how you compose approvals with code mode:

- **Decide approvals before wrapping a tool for code mode.** If a tool's
  approval decision can be made ahead of time (e.g. a policy check that
  doesn't need the specific call's args, or one you're comfortable
  pre-approving for this session), don't wrap it in `ai.RequireApproval` in
  the tool set passed to `codemode.Tool` — pass the plain, un-wrapped tool
  instead.
- **Make inline decisions OUTSIDE code mode.** If a tool genuinely needs a
  human-in-the-loop decision per call, don't route it through `run_code` at
  all — expose it as a regular tool to `GenerateText`/`StreamText` directly,
  where `ApproveToolCall` (or the suspend/resume `Approvals` flow) can
  actually gate it.

## Options

```go
type Options struct {
	Language       string // default "javascript"
	MaxOutputBytes int    // default 16384
}
```

`Language` is purely prompting — it names the language in the generated
tool description and API doc, and has no effect on what actually runs (the
`Sandbox` defines that). `MaxOutputBytes` bounds how much of `Result.Output`
is sent back to the model — see
[Output post-processing](#output-post-processing-truncation-and-logs)
below. A `nil *Options` applies both defaults. Any `MaxOutputBytes <= 0`
(zero or negative) also falls back to the default of 16384.

## Tool: wrapping tools into run_code

```go
func Tool(sandbox Sandbox, tools []ai.Tool, opts *Options) ai.Tool
```

```go
tools := []ai.Tool{searchTool, calculatorTool}
runCode := codemode.Tool(mySandbox, tools, &codemode.Options{
	Language: "python",
})

result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Search for the current Go release and compute days since its release.",
	Tools:  []ai.Tool{runCode},
})
```

The returned tool's `Name()` is always `"run_code"`. Its `Schema()` is a
fixed `{"code": string}` shape:

```json
{"type":"object","properties":{"code":{"type":"string","description":"The python code to execute."}},"required":["code"],"additionalProperties":false}
```

(`<language>` substituted with `Options.Language`, or the default
`"javascript"`). Its `Description()` is a fixed preamble naming the
language, followed by the generated [API doc](#apidoc-rendering-rules) for
`tools`, followed by a fixed usage-rules sentence.

**`Tool` panics on a duplicate tool name.** If two entries in `tools` share
the same `Name()`, `Tool` panics (`codemode: duplicate tool name %q`) at
construction time rather than silently letting the dispatch map resolve to
whichever tool happened to be last in the slice while the API doc still
documented both — that would leave one documented function the model could
never actually reach. This mirrors `ai.NewTool`'s construction-time panic on
schema-derivation failure (itself likened to `regexp.MustCompile`): a
duplicate name is a programmer error to catch at startup, not a runtime
condition to handle gracefully.

## Execute: decode, dispatch, post-process

`Execute` decodes `{"code": string}`, builds an `Env{CallTool: dispatch}`,
and calls `sandbox.Execute(ctx, code, env)`. A **sandbox error is returned
as-is, never wrapped** — the ai tool loop's usual `*ai.ToolExecutionError`
wrapping applies exactly once, at that outer layer, not a second time
inside `codemode`.

Malformed `{"code": ...}` args, on the other hand, ARE wrapped — in
`*ai.InvalidToolArgumentsError{ToolName: "run_code", Args, Cause}` — the
same typed error every other `ai.Tool` produces for bad args (see
[Tools § Execution error taxonomy](tools.md#execution-error-taxonomy)), so
`errors.As` works and `GenerateTextOpts.RepairToolCall`'s bad-args repair
path is offered a chance to fix it, exactly as it would be for a
hand-written tool.

### Unknown tool name

If the sandbox's code calls a tool name that isn't among the wrapped
`tools`, `dispatch` returns a plain error (never a panic) naming the unknown
tool and listing the available tool names, sorted alphabetically for
determinism:

```
codemode: unknown tool "lookup"; available tools: calculator, search
```

### Output post-processing: truncation and logs

`Result.Output` is truncated to `MaxOutputBytes` (default 16384) if
longer, with a `"\n[truncated]"` suffix appended. Truncation is
**rune-safe**: the cut point backs up to the start of the previous UTF-8
rune rather than naively slicing at the byte offset, so a multi-byte
character is never split in half. Exactly at the boundary (`len(Output) ==
MaxOutputBytes`), the output passes through untouched with no
`"[truncated]"` suffix.

Each entry in `Result.Logs` is then appended as its own line, prefixed
`"\nlog: "`:

```
<truncated-or-full Output>
log: fetching page 1
log: fetching page 2
```

## APIDoc rendering rules

```go
func APIDoc(language string, tools []ai.Tool) string
```

`APIDoc` renders each tool as one line:

```
functionName(args: {field: type, ...}) — description
```

- Fields come from the tool's JSON Schema `"properties"`/`"required"`:
  a field not listed in `"required"` gets a `?` suffix.
- **Object nesting: one level deep.** A property of type `"object"` is
  itself expanded inline as `{field: type, ...}`; anything **nested deeper**
  than that one level collapses to the literal type `"object"`.
- **Arrays**: `"type":"array"` renders as `itemType[]`, recursing through
  the same one-level nesting rule for the item type.
- **Property order**: JSON object keys have no defined order, so properties
  are always sorted alphabetically for deterministic output across runs.
- **No properties** (e.g. an MCP tool's opaque schema, or a schema that
  fails to parse as a JSON object): the entry renders `(args: object)`
  rather than erroring — a single malformed or opaque tool schema doesn't
  fail the whole tool description.
- `language` is accepted for future language-flavored rendering but
  currently has no effect on `APIDoc`'s own output (only on the preamble
  text `Tool` builds around it).

## Full worked example

This mirrors the fake `Sandbox` used in the package's own tests — a minimal
stand-in that "runs" a script by looking for a specific tool-call pattern,
useful as a template for wiring up a real interpreter:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/codemode"
)

// fakeSandbox is a minimal Sandbox for illustration: it doesn't actually
// interpret code, it just calls a fixed sequence of tools and collects
// their results. A real implementation would hand code off to a real
// interpreter/subprocess/container and let THAT drive env.CallTool.
type fakeSandbox struct{}

func (fakeSandbox) Execute(ctx context.Context, code string, env codemode.Env) (*codemode.Result, error) {
	var logs []string
	logs = append(logs, "starting script")

	result, err := env.CallTool(ctx, "search", json.RawMessage(`{"query":"go release date"}`))
	if err != nil {
		return nil, err
	}

	return &codemode.Result{
		Output: fmt.Sprintf("search result: %v", result),
		Logs:   logs,
	}, nil
}

func main() {
	searchTool := ai.NewTool("search", "Search the web for a query",
		func(ctx context.Context, args struct {
			Query string `json:"query"`
		}) (any, error) {
			return "Go 1.26 released 2026-08-01", nil
		})

	runCode := codemode.Tool(fakeSandbox{}, []ai.Tool{searchTool}, nil)

	out, err := runCode.Execute(context.Background(), json.RawMessage(`{"code":"search(...)"}`))
	if err != nil {
		panic(err)
	}
	fmt.Println(strings.TrimSpace(out.(string)))
	// Output includes: "search result: Go 1.26 released 2026-08-01"
	// followed by "log: starting script"
}
```

Wired into a real `GenerateText` call, `runCode` is offered to the model
like any other tool — the model never sees `search` directly, only
`run_code`, whose description embeds `search`'s signature via `APIDoc`.

## Source of truth

- [`codemode/codemode.go`](../../codemode/codemode.go) (`Sandbox`, `Env`,
  `Result`, `Options`, `Tool`)
- [`codemode/apidoc.go`](../../codemode/apidoc.go) (`APIDoc`)
- [`codemode/codemode_test.go`](../../codemode/codemode_test.go),
  [`codemode/apidoc_test.go`](../../codemode/apidoc_test.go) — the reference
  fake `Sandbox` the worked example above is modeled on

See also: [Tools](tools.md) for the underlying `ai.Tool` contract and
`RuntimeContext`; [Agents](agents.md) for another way to compose
model-driven multi-step behavior.
