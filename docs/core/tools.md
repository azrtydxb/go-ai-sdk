# Tools

Tools let a model call typed Go functions during a `GenerateText` or
`StreamText` run. `ai.NewTool[Args]` builds an `ai.Tool` from a Go struct
type and a handler function; the struct's fields are reflected into the
tool's JSON Schema automatically.

## NewTool and jsonschema tags

```go
type SearchArgs struct {
	Query string `json:"query" jsonschema:"description=The search query\\, URL-encoded if needed"`
	Unit  string `json:"unit,omitempty" jsonschema:"description=Distance unit,enum=metric|imperial"`
}

searchTool := ai.NewTool("search", "Search the knowledge base",
	func(ctx context.Context, args SearchArgs) (any, error) {
		return map[string]string{"query": args.Query}, nil
	})
```

`NewTool[Args](name, description string, fn func(context.Context, Args) (any, error)) Tool`
derives `Args`'s schema at construction time and **panics** on a schema
error (unsupported field kind, cycle, etc.) — like `regexp.MustCompile`,
this treats a bad `Args` type as a programmer error, not a runtime one.

The `jsonschema` struct tag is a comma-separated `key=value` list merged
into that field's schema fragment:

- `description=...` sets the field's `"description"`.
- `enum=a|b|c` sets `"enum"` to `["a","b","c"]`, coerced to the field's Go
  type (so an `int` field's enum values marshal as JSON numbers, not
  strings).

**Escaping:** a literal comma inside a `jsonschema` tag value must be
written `\\,` in Go source. Struct tags are Go string literals, so the
compiler's own tag-value unquoting consumes one backslash first, turning
`\\,` into a literal `\,` in the parsed tag string — which is what
`go-ai-sdk`'s own comma-splitting then treats as an escaped, literal comma.
Writing a single `\,` in source produces an *invalid* Go escape sequence,
which `reflect.StructTag.Get` silently treats as an empty tag.

## Schema derivation rules

- **Required vs optional:** a field is required unless it's a pointer type
  or its `json` tag has `omitempty`/`omitzero`. A non-pointer field with
  `omitempty` is *not* required, even though it isn't a pointer.
- **Pointers:** always optional, regardless of `omitempty`.
- **Embedded structs:** an anonymous struct field with no explicit `json`
  tag name has its fields promoted to the parent's schema, mirroring
  `encoding/json`'s flattening.

```go
type Coords struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type BookingArgs struct {
	Coords          // embedded: Lat/Lng are promoted to top-level properties
	City     string  `json:"city"`               // no omitempty, not a pointer -> required
	Notes    *string `json:"notes,omitempty"`    // pointer -> not required regardless of omitempty
	Nickname string  `json:"nickname,omitempty"` // omitempty on a non-pointer -> not required
}
```

This produces `required: ["city", "lat", "lng"]` — `notes` and `nickname`
are present as properties but not required.

## Execution error taxonomy

Three typed errors cover everything that can go wrong with a tool call:

- **`*ai.InvalidToolArgumentsError`** — the model's JSON arguments failed to
  unmarshal into `Args`. `Execute` unmarshals strictly, using
  `json.Decoder` with `DisallowUnknownFields`, and also rejects trailing
  content after the JSON value.
- **`*ai.ToolExecutionError`** — the handler function itself returned a
  non-nil error; `Execute` wraps it, preserving the original as `.Cause`.
- **`*ai.NoSuchToolError`** — the model requested a tool name that isn't in
  `Tools` (or isn't in the active set — see `ActiveTools` below). This one
  aborts the whole tool-call batch rather than being recorded per-call: see
  [Generating text](generating-text.md) for how `GenerateText` handles it.
- **`*ai.ToolApprovalDeniedError`** — recorded the same way as
  `*ai.ToolExecutionError`/`*ai.InvalidToolArgumentsError` (on
  `ToolResultRecord.Err`, never returned/raised directly) when a call
  needing approval (see [Approvals for tool execution](#approvals-for-tool-execution)
  below) was denied. `Execute` itself never runs for a denied call.

```go
divide := ai.NewTool("divide", "Divide two integers",
	func(ctx context.Context, args DivideArgs) (any, error) {
		if args.B == 0 {
			return nil, errors.New("division by zero")
		}
		return args.A / args.B, nil
	})

// Malformed JSON args -> *ai.InvalidToolArgumentsError.
_, err := divide.Execute(context.Background(), []byte(`{"a": "not a number"}`))
var invalidArgs *ai.InvalidToolArgumentsError
if errors.As(err, &invalidArgs) {
	fmt.Println("invalid args for", invalidArgs.ToolName)
}

// A handler error -> wrapped in *ai.ToolExecutionError.
_, err = divide.Execute(context.Background(), []byte(`{"a": 1, "b": 0}`))
var execErr *ai.ToolExecutionError
if errors.As(err, &execErr) {
	fmt.Println("execution failed:", execErr.Cause)
}
```

## The IsError result convention

Inside the tool loop, `GenerateText`/`StreamText` never let an
`*InvalidToolArgumentsError` or `*ToolExecutionError` abort the run — they
record it on that call's `ToolResultRecord.Err` instead, and send the
model a `provider.ToolResultPart` with `IsError: true` and the error's
`.Error()` string as the result, so the model sees the failure and can
retry or adapt. Only an unresolved `*NoSuchToolError` aborts the batch.

## ActiveTools

`ActiveTools`, when non-nil, restricts which of `Tools` are *offered* to the
model **and** which are treated as known during execution — a tool present
in `Tools` but outside `ActiveTools` is treated as unknown
(`*NoSuchToolError`) if the model somehow still calls it. A `nil`
`ActiveTools` (the default) means every tool in `Tools` is active.

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Search for something.",
	Tools:  []ai.Tool{searchTool, deleteTool},
	// Only "search" is offered to the model, even though both tools
	// are present in Tools.
	ActiveTools: []string{"search"},
})
```

## RepairToolCall

`RepairToolCall`, when set, is offered a chance to fix a failing call
*once* per original call — an unknown tool name, or an
`*InvalidToolArgumentsError` from `Execute`:

```go
RepairToolCall: func(ctx context.Context, call ai.ToolCallRecord, toolErr error) (ai.ToolCallRecord, bool) {
	if call.Name == "lookup_record" {
		// The model used a slightly different tool name; map it
		// back to the real one.
		return ai.ToolCallRecord{ID: call.ID, Name: "lookup", Args: call.Args}, true
	}
	if _, ok := toolErr.(*ai.InvalidToolArgumentsError); ok {
		fixed, _ := json.Marshal(LookupArgs{ID: "42"})
		return ai.ToolCallRecord{ID: call.ID, Name: call.Name, Args: fixed}, true
	}
	return call, false
},
```

**The single-shot rule:** whatever `RepairToolCall` returns is re-validated
(and, for bad-args repairs, re-executed) exactly once. If the repaired call
fails again — still an unknown tool, or `Execute` fails again —
`RepairToolCall` is *not* invoked a second time for that original call; the
second failure's normal semantics apply (`*NoSuchToolError` aborts the
batch, `*InvalidToolArgumentsError` is recorded on the result).

**Repair × approval ordering.** A bad-args repair is re-checked against
`ApprovalRequirer` using the *repaired* call's tool and args, before it
executes — never against whatever decision (if any) let the *original* call
reach execution. Two ways this matters:

- Repair renames the call to a *different* tool that requires approval,
  while the original tool needed no decision at all.
- Repair changes the *args* of an already-approved approval-requiring call —
  the approval that was granted covered the original args, not the repaired
  ones.

Either way, there is no suspension possible mid-execution — approval
decisions are only ever resolved before a batch starts executing, not
partway through one call's repair retry — so a repaired call that now
requires approval is never executed and never silently allowed through on
the strength of a stale decision. It is recorded on that call's
`ToolResultRecord.Err` as `&ai.ToolApprovalDeniedError{ToolName, Reason:
"approval required for repaired call"}`, the same `IsError`-on-the-wire
shape as any other denial (see
[Approvals for tool execution](#approvals-for-tool-execution) below).

## Multi-modal tool results

A tool's `Execute` may return an `ai.ToolResultContent` (or
`*ai.ToolResultContent`) instead of a plain value when it wants to attach
one or more images alongside — or instead of — text, e.g. a screenshot
tool, an image-generation tool, or a chart renderer:

```go
screenshotTool := ai.NewTool("take_screenshot", "Capture the current screen",
	func(ctx context.Context, args ScreenshotArgs) (any, error) {
		img := captureScreen() // provider.GeneratedImage
		return ai.ToolResultContent{
			Text:   "Captured a 1280x720 screenshot.",
			Images: []provider.GeneratedImage{img},
		}, nil
	})
```

A tool that never needs images can keep returning a plain string (or any
other JSON-marshalable value) — `ToolResultContent` is opt-in.

**Provider support for the `Images` half is uneven**, since not every wire
format has an image slot in a tool result:

| Provider | Support |
|---|---|
| anthropic | Native: the `tool_result` content block's `"content"` becomes an array — one `{"type":"text"}` block (only when `Text` is non-empty) followed by one `{"type":"image","source":{...}}` block per entry in `Images`. |
| bedrock (Converse) | Native, same shape: the `toolResult` block's `"content"` array gets a `{"text":...}` entry (only when `Text` is non-empty) followed by one `{"image":{...}}` entry per entry in `Images`. |
| openaicompat-based providers, geminicompat, cohere, mistral | Text-projection only: `ToolResultContent` is projected down to its `Text` field — `Images` is silently dropped. |

If a script must run identically across a text-projection provider and an
image-capable one, prefer text-describable results, or attach the image via
a separate, provider-agnostic mechanism instead (e.g. a `FilePart` on a
subsequent user message — see [Media § FilePart attachment matrix](media.md#filepart-attachment-matrix)).

## Approvals for tool execution

Some tools should pause for human (or policy) sign-off before they run —
sending an email, deleting a record, spending money. `ai.RequireApproval`
wraps any `ai.Tool` to require approval before each call executes:

```go
deleteTool := ai.NewTool("delete_record", "Delete a record by ID",
	func(ctx context.Context, args DeleteArgs) (any, error) {
		return db.Delete(args.ID)
	})

// Every call to delete_record requires approval.
guardedDelete := ai.RequireApproval(deleteTool)
```

`RequireApproval`'s optional second argument narrows *which* calls need
approval, based on the call's own arguments:

```go
// Only calls targeting a record in the "prod" namespace require approval.
guardedDelete := ai.RequireApproval(deleteTool, func(ctx context.Context, args json.RawMessage) bool {
	var a DeleteArgs
	_ = json.Unmarshal(args, &a)
	return strings.HasPrefix(a.ID, "prod-")
})
```

Under the hood, the wrapped tool implements `ai.ApprovalRequirer`:

```go
type ApprovalRequirer interface {
	ApprovalRequired(ctx context.Context, args json.RawMessage) bool
}
```

`GenerateText`/`StreamText` check every tool call's underlying `Tool` for
this interface — a plain `ai.NewTool`-built tool (never wrapped in
`RequireApproval`) never implements it, so it's never gated. This also
means the interface works for any `ai.Tool`, including ones adapted from an
MCP server — wrap the adapted tool the same way.

### Decision order: Approvals, then ApproveToolCall, then pending

For each call whose tool reports `ApprovalRequired() == true`, a decision is
resolved in this order:

1. **`GenerateTextOpts.Approvals`** — checked first, matched by
   `ToolCallID`, but **only for the RESUME batch** (the unanswered
   assistant tool-call batch at the end of `Messages` at the start of this
   call — see [Resumable flow](#resumable-flow-suspend-then-resume) below).
   For any batch arising LATER within the same run, `Approvals` is not
   consulted at all — go straight to `ApproveToolCall`. This scoping exists
   because `Approvals` matches by `ToolCallID` alone, and some providers
   (e.g. geminicompat) synthesize deterministic IDs from a call's name and
   index — a later batch's call can legitimately reuse an ID an earlier
   `Approvals` entry already answered, and matching it there too would
   auto-approve a call no one actually decided.
2. **`GenerateTextOpts.ApproveToolCall`** — called whenever `Approvals`
   didn't apply or had no matching decision. Signature:
   `func(ctx context.Context, req ai.ApprovalRequest) (ai.ApprovalDecision, bool)`.
   Return `(decision, true)` to decide inline; `(_, false)` to leave it
   pending.
3. **Pending** — if neither source produces a decision, the call is left
   undecided, which suspends the whole batch (see below).

```go
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

### Batch atomicity

If a step's tool-call batch has ANY call still undecided after checking
`Approvals` then `ApproveToolCall`, **no call in that batch executes** —
not the undecided one, and not any other call in the same batch that needed
no approval or was already decided. The whole batch suspends together; see
[Suspension result shape](#suspension-result-shape) below.

### Denial: ToolApprovalDeniedError as an IsError tool result

A denied call (an `ApprovalDecision{Approved: false}`) never reaches
`Execute` — instead, `*ai.ToolApprovalDeniedError` is recorded on that
call's `ToolResultRecord.Err`, and the model receives a
`provider.ToolResultPart` with `IsError: true` whose text is the error's
message:

```
ai: tool "delete_record" execution denied: not allowed
```

(`Reason` is omitted from the message when empty: `ai: tool "delete_record" execution denied`.)
This is the same `IsError` convention every other tool-execution error uses
— see [The IsError result convention](#the-iserror-result-convention) above
— so a denial looks like any other tool failure to the model, which can
explain the situation to the user or try something else.

### Inline flow: ApproveToolCall decides synchronously

The simplest flow never suspends at all — `ApproveToolCall` answers every
approval-needing call as it's encountered:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Delete the record for user 42.",
	Tools:  []ai.Tool{guardedDelete},
	MaxSteps: 2,
	ApproveToolCall: func(ctx context.Context, req ai.ApprovalRequest) (ai.ApprovalDecision, bool) {
		approved := policyAllows(req.Call) // your own logic, e.g. a UI prompt or policy check
		return ai.ApprovalDecision{ToolCallID: req.Call.ID, Approved: approved}, true
	},
})
```

### Suspension result shape

When a batch suspends, `*ai.GenerateTextResult` (or `TextStream`, for
`StreamText`) reports:

- **`PendingApprovals`** (`[]ai.ApprovalRequest`) — one entry per
  approval-needing call left undecided, in call order.
- **`Messages`** — ends with the assistant message carrying the suspended
  tool-call batch. This is a complete, round-trippable transcript: resend
  it as `Messages` on the next call (with `Approvals` set) to resume — no
  provider's wire format is left with a dangling unanswered tool call in
  the interim, because nothing was sent to the model yet for this batch.
- **`FinishReason`** is `provider.FinishToolCalls` — the step's real finish
  reason, not an error sentinel.
- Not an error: suspension returns `err == nil`, and `OnFinish` still fires
  (with `PendingApprovals` populated) — see
  [Generating text § Approvals, PendingApprovals, and resume](generating-text.md#approvals-pendingapprovals-and-resume).

### Resumable flow: suspend, then resume

```go
// First call: no Approvals/ApproveToolCall configured for this batch, so
// the call needing approval suspends.
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:    model,
	Prompt:   "Delete the record for user 42.",
	Tools:    []ai.Tool{guardedDelete},
	MaxSteps: 2,
})
if err != nil {
	log.Fatal(err)
}

if len(result.PendingApprovals) > 0 {
	fmt.Println("awaiting approval for:", result.PendingApprovals[0].Call.Name)

	// ... later, once a human/policy decision is available ...

	resumed, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
		Model:    model,
		Messages: result.Messages, // ends in the unanswered tool-call batch
		Tools:    []ai.Tool{guardedDelete},
		MaxSteps: 2,
		Approvals: []ai.ApprovalDecision{
			{ToolCallID: result.PendingApprovals[0].Call.ID, Approved: true},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(resumed.Text)
}
```

Resuming and suspending again immediately (no decision supplied for the
same call twice in a row) returns an empty `Steps` and the same
`FinishReason`/`Messages` as before, without ever calling the model — see
[Generating text § Resume-from-Messages is its own capability](generating-text.md#resume-from-messages-is-its-own-capability)
for why this "resume detection" is more general than approvals alone.

**Approvals do not apply to the `Output` tool-mode fallback's synthetic
call.** When `GenerateTextOpts.Output` is set and the model has no native
JSON mode, `GenerateText` forces a single injected output-schema tool call
that it decodes directly — that call is never checked against
`ApprovalRequirer`, `Approvals`, or `ApproveToolCall`, even if one of your
own tools happens to share a name with the injected one (it doesn't; the
injected tool's name is chosen internally). See
[Generating text § Output modes](generating-text.md#output-modes).

## RuntimeContext

`ai.RuntimeContext` (a `map[string]any`) is an arbitrary bag of application
values made available to tools during execution — request-scoped state
(a user ID, a database transaction, a request-tracing ID) that a
`Tool.Execute` function needs but shouldn't have to be threaded through
`Args`.

```go
opts := ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Look up my account balance.",
	Tools:  []ai.Tool{balanceTool},
	RuntimeContext: ai.RuntimeContext{
		"userID": "u_42",
	},
}

balanceTool := ai.NewTool("get_balance", "Get the current user's account balance",
	func(ctx context.Context, args struct{}) (any, error) {
		rc := ai.RuntimeContextFrom(ctx)
		userID, _ := rc["userID"].(string)
		return db.Balance(userID)
	})
```

Set `GenerateTextOpts.RuntimeContext` to have it installed on the `ctx`
passed to `Tool.Execute` for the run — retrieve it inside a tool (or an
`ApprovalRequirer.ApprovalRequired`, or an `ApproveToolCall` callback, both
of which receive the same `ctx`) with `ai.RuntimeContextFrom(ctx)`. It is
installed **once**, before the tool loop begins (both `GenerateText` and
`StreamText`), so every step and every resumed batch inside one call sees
the identical value. `RuntimeContextFrom` returns `nil` when no
`RuntimeContext` was configured, or when `ctx` is unrelated to any
`GenerateText`/`StreamText` call.

`RuntimeContextFrom` is unexported-key-backed, so the only way to read it
is through the accessor — nothing outside package `ai` (or code holding the
right `ctx`) can install a spoofed value.

## Tools from MCP servers

An [MCP](../mcp.md) server's tools can be adapted into `ai.Tool`s and passed
into `Tools` the same way as any hand-written tool — see the
[MCP guide](../mcp.md) for the client walkthrough and the tools adapter.

## Source of truth

- [`ai/tool.go`](../../ai/tool.go)
- [`ai/tool_result_content.go`](../../ai/tool_result_content.go) (`ToolResultContent`)
- [`ai/generate_text.go`](../../ai/generate_text.go) (tool loop, `runToolCalls`,
  `runApprovalAwareToolCalls`)
- [`ai/approval.go`](../../ai/approval.go) (`ApprovalRequirer`,
  `RequireApproval`, `ApprovalRequest`, `ApprovalDecision`)
- [`ai/runtime_context.go`](../../ai/runtime_context.go) (`RuntimeContext`,
  `RuntimeContextFrom`)
- [`ai/errors.go`](../../ai/errors.go) (including `ToolApprovalDeniedError`)
- [`internal/schema/schema.go`](../../internal/schema/schema.go)

See also: [Generating text § Approvals, PendingApprovals, and resume](generating-text.md#approvals-pendingapprovals-and-resume)
for the full opts/result field reference; [Streaming § Suspension in streams](streaming.md#suspension-in-streams)
for `StreamText`'s suspension behavior; [Agents](agents.md) for
`Agent.ApproveToolCall`/`RunOpts.Approvals` passthrough and
`Agent.RuntimeContext` sub-agent scoping.
