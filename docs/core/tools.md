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

## Tools from MCP servers

An [MCP](../mcp.md) server's tools can be adapted into `ai.Tool`s and passed
into `Tools` the same way as any hand-written tool — see the
[MCP guide](../mcp.md) for the client walkthrough and the tools adapter.

## Source of truth

- [`ai/tool.go`](../../ai/tool.go)
- [`ai/generate_text.go`](../../ai/generate_text.go) (tool loop, `runToolCalls`)
- [`ai/errors.go`](../../ai/errors.go)
- [`internal/schema/schema.go`](../../internal/schema/schema.go)
