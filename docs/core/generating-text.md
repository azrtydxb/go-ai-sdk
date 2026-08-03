# Generating text

`ai.GenerateText` is the core entry point for calling a language model. It
takes a `GenerateTextOpts` and returns a `*GenerateTextResult` once the
model (and, if tools were called, the resulting tool loop) has finished.

## Options, field by field

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:         model,
	System:        "You are a terse assistant.",
	Prompt:        "Summarize the Go scheduler in one sentence.",
	MaxTokens:     ptr(200),
	Temperature:   ptr(0.7),
	TopP:          ptr(0.9),
	StopSequences: []string{"\n\n"},
	MaxRetries:    ptr(3),
})
```

where `ptr` is a small generic helper (`go-ai-sdk` doesn't ship one, since a
one-liner suffices):

```go
func ptr[T any](v T) *T { return &v }
```

- **`Model`** (`provider.LanguageModel`, required) — the model to call, from
  any provider's `Model(id string)`.
- **`System`** (`string`, optional) — prepended as a system message.
- **`Prompt`** / **`Messages`** — exactly one of the two must be set.
  `Prompt` wraps a single user turn; `Messages` supplies the full
  conversation directly. Setting both, or neither, is an error returned
  from `GenerateText` (see below).
- **`MaxTokens`, `Temperature`, `TopP`, `StopSequences`** — passed through to
  the provider's wire request; each is optional and provider-defined when
  unset.
- **`TopK`, `PresencePenalty`, `FrequencyPenalty`, `Seed`, `Headers`** — see
  [Additional call settings](#additional-call-settings-topk-penalties-seed-headers)
  below.
- **`MaxRetries`** (`*int`, default `2`) — the number of retries around each
  model call (see [Retries](#retries-and-retryerror) below).

## Additional call settings: TopK, penalties, Seed, Headers

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:            model,
	Prompt:           "Write a short poem about Go channels.",
	TopK:             ptr(40),
	PresencePenalty:  ptr(0.5),
	FrequencyPenalty: ptr(0.5),
	Seed:             ptr(int64(42)),
	Headers:          map[string]string{"x-request-id": "abc123"},
})
```

These five settings are threaded through unchanged to the identically-named
`provider.Call` fields (`ai/options.go`'s `buildCall`) — full per-provider
support and wire-name mapping lives in each field's doc comment in
[`provider/call.go`](../../provider/call.go). Summarized:

| Setting | Supported by | Ignored by (silently, no wire param, no error) |
|---|---|---|
| `TopK` | anthropic, geminicompat (Google/Vertex), cohere (wire field `k`) | openaicompat-based providers (OpenAI, Azure, Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity), mistral, bedrock |
| `PresencePenalty` / `FrequencyPenalty` | openaicompat-based providers, cohere, mistral (wire fields `presence_penalty`/`frequency_penalty`) | anthropic, geminicompat, bedrock |
| `Seed` | openaicompat-based providers (`seed`), cohere (`seed`), mistral (`random_seed`) | anthropic, geminicompat, bedrock |
| `Headers` | every language-model request path: openaicompat, geminicompat, anthropic, cohere, mistral, bedrock | not yet implemented (this wave) by any embedding or media (image/speech/transcription) request path |

An "ignored by" provider drops the field entirely — nothing is sent on the
wire, and no error is returned. `ProviderOptions` can still reach an
otherwise-unsupported provider's native parameter name directly if that
provider's API happens to accept it undocumented (e.g.
`{"mistral": {"top_k": 5}}`); this table only covers parameters the SDK maps
by name.

**`Headers` precedence:** entries are applied AFTER the provider sets its own
authentication header(s), so a `Headers` entry can never override auth — a
key that case-insensitively matches the header the provider uses for
authentication (`Authorization`, `x-api-key`, `x-goog-api-key`, ...) is
silently skipped; every other entry wins over anything the SDK would
otherwise set. Bedrock is a special case because requests are SigV4-signed:
an entry whose key case-insensitively starts with `x-amz-` is set BEFORE
signing (so it participates in the signature), and every other entry is set
AFTER signing (reaches the wire unsigned, not covered by the signature).

Getting `Prompt`/`Messages` exclusivity wrong returns an error immediately,
without calling the model:

```
ai: exactly one of Prompt or Messages must be set
```

A `nil` `Model` similarly returns `ai: GenerateTextOpts.Model is required`.

## The multi-step tool loop

When `Tools` are set and the model requests a tool call, `GenerateText`
executes the tools and calls the model again automatically — this is the
"tool loop." Two options bound how many steps it runs for:

- **`MaxSteps`** (`int`, default `1`) — a hard cap on the number of steps.
  A step is one model call (plus, if it requested tools, executing them). At
  the default of 1, a tool-calling response ends the run after that single
  step, with the tool calls left unexecuted in the result.
- **`StopWhen`** (`func(steps []Step) bool`, optional) — evaluated after
  EVERY completed step, whether or not that step requested tool calls (this
  is what makes `LoopFinished`, below, meaningful to compose with other
  conditions inside a custom `StopWhen`). That said, a step with no tool
  calls always ends the loop naturally regardless of what `StopWhen` returns
  for it — `StopWhen` cannot make the loop continue past a step the model
  didn't request further tool calls in. Returning `true` stops the loop.

```go
weatherTool := ai.NewTool("get_weather", "Get the current weather for a city",
	func(ctx context.Context, args WeatherArgs) (any, error) {
		return map[string]string{"city": args.City, "conditions": "sunny"}, nil
	})

result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "What's the weather in Paris? Then summarize it.",
	Tools:  []ai.Tool{weatherTool},
	// MaxSteps is a hard cap; StopWhen decides whether to stop earlier.
	// If MaxSteps is left at 0 and StopWhen is set, the hard cap
	// defaults to 16 instead of the usual default of 1.
	StopWhen: ai.StepCountIs(4),
})
```

`ai.StepCountIs(n)` is a ready-made `StopWhen` that stops once at least `n`
steps have completed. Two more ready-made helpers cover the other common
conditions:

- **`ai.HasToolCall(names ...string)`** — stops when the LAST completed step
  called any of the named tools. With no names given, it stops when the last
  step called any tool at all, regardless of name.
- **`ai.LoopFinished()`** — stops when the LAST completed step made no tool
  calls: the same condition that already ends the loop naturally. It exists
  mainly for composing inside a custom `StopWhen` closure alongside other
  conditions (on its own it's redundant with the loop's natural end).

```go
StopWhen: func(steps []ai.Step) bool {
	// Stop at 10 steps, or as soon as the model calls "finalize_answer" —
	// whichever comes first.
	return ai.StepCountIs(10)(steps) || ai.HasToolCall("finalize_answer")(steps)
},
```

**The default-cap-16 rule:** if `MaxSteps` is left unset (`0`) and
`StopWhen` is non-nil, the effective hard cap defaults to 16 instead of the
usual default of 1 — so setting only `StopWhen` gives you a working
multi-step loop without also having to compute a `MaxSteps`. `MaxSteps`
still applies as the hard cap regardless of what `StopWhen` decides.

## PrepareStep

`PrepareStep` runs before each model call, with the zero-based step index
and the `StepPlan` about to be used — `Call` is the request about to be
sent, `Model` is the model that will send it (`opts.Model` on step 0, or
whatever a prior `PrepareStep` call last swapped to). Return `(plan, true)`
to use the returned plan for that step, or `(_, false)` to leave it
unchanged.

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:    planningModel,
	Prompt:   "Plan then execute a short task.",
	StopWhen: ai.StepCountIs(3),
	PrepareStep: func(stepIndex int, plan ai.StepPlan) (ai.StepPlan, bool) {
		if stepIndex == 1 {
			// Swap to a cheaper model starting at step 1; the swap
			// persists for every later step until PrepareStep swaps
			// again.
			plan.Model = cheaperModel
			return plan, true
		}
		return plan, false
	},
})
```

**Model-swap persistence rule:** setting `StepPlan.Model` swaps the model
used for that step's call *and every step after it*, until `PrepareStep`
swaps again. This is a deliberate divergence from a strictly per-step swap:
a swap made at step N doesn't need to be re-asserted at every later step to
"stick," which matches the common case of routing to a cheaper model partway
through a run rather than alternating models step by step.

**Output and NativeJSON capability:** a model swapped in via `PrepareStep` is
not re-checked against `Output`'s `NativeJSON` capability requirement — the
output strategy is fixed from `opts.Model` at entry. Swapping to a model
without native JSON mid-loop leaves the schema unenforced on that provider.

## OnStepFinish

`OnStepFinish`, when set, is called after each step completes (including the
final step) in both `GenerateText` and `StreamText`, with the finished
`Step`. It doesn't return an error — errors aren't propagated from the
callback.

```go
OnStepFinish: func(step ai.Step) {
	fmt.Println("step finished, finish reason:", step.FinishReason)
},
```

**StreamText abandonment caveat:** in `StreamText`, the callback fires only
once a step's `Parts()` iteration has run to completion. If the consumer
stops ranging over `Parts()` before that — e.g. breaking out of the loop
right after observing that step's `FinishPart` — `OnStepFinish` does not
fire for that step, even though `FinishPart` itself was already delivered.

## OnFinish and OnError

`OnFinish`, when set, is called once with the call's result after it
completes successfully. `OnError`, when set, is called with the call's
terminal error.

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Say hello.",
	OnStepFinish: func(step ai.Step) {
		fmt.Println("step finished, finish reason:", step.FinishReason)
	},
	OnFinish: func(result *ai.GenerateTextResult) {
		fmt.Println("total usage:", result.Usage.TotalTokens)
	},
	OnError: func(err error) {
		fmt.Println("call failed:", err)
	},
})
```

In `GenerateText`, `OnFinish` fires right before the call returns, with the
same `*GenerateTextResult` that's returned. In `StreamText`, it fires at the
natural end of `TextStream.Parts()` iteration — never on a step that ended
in an error, nor if the consumer abandons iteration before the stream ends
naturally.

`OnError` covers, in `StreamText`, errors that end `Parts()` iteration
abnormally (a mid-stream provider error, or a tool-loop error such as an
unknown tool). In `GenerateText`, the function's returned error already
fully signals failure to the caller; `OnError` additionally fires with that
same error, for symmetry with `StreamText`, so code wiring both APIs through
one callback doesn't have to special-case `GenerateText`.

**Validation-error exclusion rule:** neither `OnError` nor `OnFinish` fires
for argument-validation errors — a `nil` `Model`, or `Prompt`/`Messages`
misuse. Those are reported solely via the function's returned error, in both
`GenerateText` and `StreamText`. In `StreamText`'s case this is structural:
validation happens before the first model call, so there's no started call
for `OnError` to describe; `GenerateText` applies the same exclusion for
consistency, even though it could technically fire `OnError` there too.

## OnAbort

`OnAbort`, when set, is consulted only by `StreamText` — `GenerateText` has
no notion of an abandoned or mid-flight iteration, so it never calls
`OnAbort`. It fires exactly once per `TextStream`, before the stream's
internal `Close`, in either of two cases:

- the consumer abandons `TextStream.Parts()` early — stops ranging over it
  (e.g. via `break`) before the tool loop would otherwise have ended
  naturally or with an error;
- the context passed to `StreamText` is canceled (or its deadline exceeded)
  while a step's stream is in flight, surfacing as that step's
  `stream.Err()`.

```go
stream, err := ai.StreamText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Count to a million.",
	OnAbort: func() {
		fmt.Println("stream abandoned or context canceled")
	},
})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

for part := range stream.Parts() {
	if _, ok := part.(provider.TextDelta); ok {
		break // abandoning iteration early triggers OnAbort, not OnFinish/OnError
	}
}
```

**Mutual exclusion with `OnFinish`/`OnError`:** `OnAbort` never fires on
natural completion (`StopWhen`/`MaxSteps`/no more tool calls) — that's
`OnFinish`'s case — nor does it fire together with `OnError` for the same
event: a ctx-cancellation mid-stream fires `OnAbort` only, while any other
mid-stream error (a real provider failure, not caused by ctx) fires
`OnError` only. Abandoning iteration early is likewise never accompanied by
an error — `Err()` reports `nil` in that case, same as before `OnAbort`
existed — so only `OnAbort` fires for it.

## Output modes

`GenerateTextOpts.Output` selects a structured-output mode for
`GenerateText` — decode the model's final text straight into a Go value,
without switching to `ai.GenerateObject[T]`. It's `GenerateText`-only:
`StreamText` returns `ErrOutputWithStreamText` immediately when `Output` is
set (partial-output streaming is future work — see
[Migrating from the Vercel AI SDK](../migrating-from-vercel-ai-sdk.md)).

Four constructors build an `Output`:

- **`OutputObject[T]()`** — decode into a single `T`, schema derived the
  same way `ai.GenerateObject[T]` does.
- **`OutputArray[T]()`** — decode into a `[]T`. The requested schema wraps
  the per-element schema in an object with a single `"elements"` array
  property (most providers' schema-constrained JSON modes require a
  top-level object, not a bare array) — this is transparent to you; `OutputAs`
  still gives back a plain `[]T`.
- **`OutputChoice(choices ...string)`** — decode into one of `choices`,
  enforced via a JSON schema enum.
- **`OutputJSON()`** — schemaless: decode into `any` (`map[string]any`,
  `[]any`, `string`, `float64`, `bool`, or `nil`, per
  `encoding/json`'s default unmarshal-into-any rules). Always requests
  `ResponseFormat{Type: "json"}` with no schema, regardless of
  `Model.Capabilities().NativeJSON`.

```go
type Recipe struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
}

result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Give me a simple pancake recipe.",
	Output: ai.OutputObject[Recipe](),
})
if err != nil {
	log.Fatal(err)
}

recipe, err := ai.OutputAs[Recipe](result)
if err != nil {
	log.Fatal(err)
}
fmt.Println(recipe.Name)
```

`OutputAs[T](result)` extracts `result.Output` (typed `any` on
`*GenerateTextResult`) as a concrete `T`, returning a descriptive error
(never a panic) if `result.Output` is `nil` or its dynamic type doesn't
match `T`. For `OutputArray[T]`, extract as `[]T` (not `T`):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three pancake toppings.",
	Output: ai.OutputArray[Recipe](),
})
recipes, err := ai.OutputAs[[]Recipe](result)
```

`OutputChoice` decodes into a plain `string`, and `OutputJSON` into `any`:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Is this review positive or negative? 'Loved it!'",
	Output: ai.OutputChoice("positive", "negative"),
})
choice, err := ai.OutputAs[string](result)
```

### SchemaDescription

When `Output` is set, `GenerateTextOpts.SchemaDescription` (optional) describes
the expected output schema. It is used as the injected output tool's `Description`
in the tool-mode fallback and passed as `ResponseFormat` description where
providers support one. This is useful for guiding the model on the expected
structure when using tool-mode fallback with models that don't have native JSON
support.

### Native JSON vs tool-mode fallback

Just like `GenerateObject`, the wire strategy depends on
`opts.Model.Capabilities().NativeJSON` (see
[Structured output § Native JSON vs tool mode](structured-output.md#native-json-vs-tool-mode)
for the full capability matrix):

- **Native JSON** (`NativeJSON: true`) — the schema is sent via
  `Call.ResponseFormat`; the loop runs its normal single step.
- **Tool mode** (`NativeJSON: false`) — a single tool is injected and
  `ToolChoice` forced to it, exactly like `GenerateObject`'s fallback. This
  requires `opts.Tools` to be empty: if the model has no native JSON mode
  **and** you've also set `Tools`, `GenerateText` returns
  `ErrOutputRequiresJSONOrNoTools` up front, before calling the model — the
  injected output tool can't coexist with your own tools on a model that has
  no other way to force a schema-constrained response.

```go
_, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  anthropicModel, // NativeJSON: false
	Prompt: "What's the weather, structured please.",
	Output: ai.OutputObject[Recipe](),
	Tools:  []ai.Tool{weatherTool},
})
// err is ai.ErrOutputRequiresJSONOrNoTools
```

In the tool-mode fallback, the forced output-tool call is never executed as
a real tool: `GenerateText` decodes `Output` straight from that call's raw
arguments and ends the loop in exactly one step, without running
`OnToolExecutionStart`/`OnToolExecutionEnd` for it (see
[Lifecycle callbacks](#lifecycle-callbacks-model-call-and-tool-execution)
below). That step also does not evaluate `StopWhen` — the forced call *is*
the structured output, so the loop ends there unconditionally (see
`StopWhen`'s doc comment for this one exception).

The forced call is scrubbed from the result so it can't be mistaken for a
real, unanswered tool call:

- `FinishReason` is never `"tool-calls"` for it — the underlying response's
  finish reason is kept when it's something else (e.g. `"length"`);
  `"tool-calls"` itself is mapped to `"stop"`.
- `ToolCalls` is empty on both the final `Step` and the returned
  `*GenerateTextResult`.
- `Messages` ends with the assistant message carrying that tool call,
  followed by a synthetic `RoleTool` message answering it — a single
  `ToolResultPart` whose `Result` is the call's own args JSON, as a string.
  This keeps the transcript well-formed for a round-trip resend: no
  provider's wire format is left with a dangling (unanswered) tool call.

If the model calls some other tool instead of the injected output-schema
tool (or makes no tool calls at all when `Output`'s tool-mode fallback is
in effect), `GenerateText` returns a `*ai.NoObjectGeneratedError` with
`RawText` set to the response's text, rather than silently decoding the
wrong call's arguments. In the tool-mode fallback, if the model emits the
output tool more than once in one response, only the first matching call is
decoded and answered.

A decode failure (the model's final text isn't valid JSON, or doesn't match
`Output`'s schema) also returns a `*ai.NoObjectGeneratedError` — the same
type `GenerateObject` returns — with `RawText` set to what the model
actually produced. For `OutputChoice`, this includes a value outside the
configured choice set: the schema's `enum` constraint isn't necessarily
enforced by tool-mode providers, so `decode` checks membership itself.
`OutputChoice()` called with zero choices is a configuration error returned
up front (before any model call), rather than requesting an unsatisfiable
`{"enum":[]}` schema.

`GenerateObject`/`StreamObject` and `Output` modes solve overlapping
problems; see
[Structured output § GenerateObject vs Output modes](structured-output.md#generateobject-vs-output-modes)
for when to reach for which.

## Lifecycle callbacks: model call and tool execution

Two more callback pairs bracket the lower-level events inside a step, below
`OnStepFinish`: one around each underlying model request, one around each
tool execution.

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "What's the weather in Paris?",
	Tools:  []ai.Tool{weatherTool},
	OnModelCallStart: func(stepIndex int, call provider.Call) {
		fmt.Println("model call starting, step", stepIndex)
	},
	OnModelCallEnd: func(end ai.ModelCallEnd) {
		fmt.Println("model call finished, step", end.StepIndex, "err:", end.Err)
	},
	OnToolExecutionStart: func(stepIndex int, call ai.ToolCallRecord) {
		fmt.Println("executing tool", call.Name)
	},
	OnToolExecutionEnd: func(stepIndex int, result ai.ToolResultRecord, err error) {
		fmt.Println("tool finished", result.Name, err)
	},
})
```

- **`OnModelCallStart(stepIndex int, call provider.Call)`** fires exactly
  once per step, immediately before the underlying model request — before
  the FIRST attempt, regardless of how many retries that step's call ends
  up needing. Fires in both `GenerateText` and `StreamText`.
- **`OnModelCallEnd(end ModelCallEnd)`** fires exactly once per step, after
  the FINAL attempt (success or retry exhaustion). In `GenerateText`,
  `end.Response` is the provider response (`nil` on error) and `end.Err` —
  when non-nil — is the SAME error `GenerateText` itself returns for that
  failure (retry exhaustion already translated to `*ai.RetryError`, never
  the raw retry-internal error). In `StreamText`, `end.Response` is always
  `nil`; `end.Usage`/`end.FinishReason` carry what that step's `FinishPart`
  reported. **`OnModelCallEnd` does NOT fire on `StreamText`'s abort
  path** — the consumer abandoning `Parts()` iteration early, or ctx
  cancellation — `OnAbort` covers that instead, so a step's Start/End pair
  either both fire, or (on abort) neither Err-bearing End fires alone.
- **`OnToolExecutionStart(stepIndex int, call ToolCallRecord)`** /
  **`OnToolExecutionEnd(stepIndex int, result ToolResultRecord, err error)`**
  bracket each tool call's `Execute`. Exactly one Start/End pair fires per
  tool call record, including when `RepairToolCall` retries a failed
  `Execute` once — the whole execute-and-maybe-repair sequence counts as one
  execution, not two. Neither callback fires for a call that never reaches
  `Execute` at all (e.g. an unknown-tool call that aborts the batch with no
  successful repair), nor for the tool-mode `Output` fallback's synthetic
  forced call (see [Output modes](#output-modes) above).

  **ID/Name caveat after repair:** if `RepairToolCall`'s bad-args repair
  path changes a call's `ID` or `Name`, the pair does NOT agree on those
  fields — `OnToolExecutionStart` fires with the ORIGINAL (pre-repair) `ID`/
  `Name` (before `Execute` is first attempted), while
  `OnToolExecutionEnd`'s `ToolResultRecord` carries the REPAIRED `ID`/`Name`
  (what was actually executed). Correlate the pair by call order within the
  step, not by `ID`, if `RepairToolCall` may rename calls.

## Approvals, PendingApprovals, and resume

`GenerateTextOpts.ApproveToolCall`/`.Approvals` and
`GenerateTextResult.PendingApprovals` implement tool-execution approvals —
see [Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
for the full walkthrough (decision order, batch atomicity, denial shape,
a complete resume example). Summarized as an opts/result reference:

- **`ApproveToolCall func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, bool)`**
  — decides approval-needing calls inline. Checked only after `Approvals`
  has no matching decision for that call's `ToolCallID`.
- **`Approvals []ApprovalDecision`** — out-of-band decisions, matched by
  `ToolCallID`. Checked first, both against the resumed batch at the start
  of `Messages` (see below) and against any approval-needing call arising
  later in the same run.
- **`PendingApprovals []ApprovalRequest`** (on the result) — non-empty when
  the loop suspended because some call(s) needed approval and neither
  `Approvals` nor `ApproveToolCall` produced a decision. Not an error:
  `err == nil`, `OnFinish` still fires, `FinishReason` is the step's real
  finish reason (`provider.FinishToolCalls`).

### Resume-from-Messages is its own capability

`Messages`, when its **last** message is an assistant message carrying tool
calls (necessarily unanswered, since nothing follows it), is treated as a
**resume**: both `GenerateText` and `StreamText` run that trailing batch
first — through the same approval rules as any other batch — before making
any model call, then proceed with the loop as usual. This resume-detection
is new loop-entry behavior in its own right, independent of whether
approvals are involved at all: **any** transcript that ends in an
unanswered assistant tool-call batch becomes resumable this way, including
one built up manually rather than returned from a suspended
`GenerateText`/`StreamText` call.

If that leading batch is *itself* still pending after resume (no decision
available for some call in it), the run suspends again immediately —
`Steps` is empty, `Messages` is unchanged, `FinishReason` is
`provider.FinishToolCalls` — without ever calling the model. This is
symmetric with a fresh (non-resumed) suspension in every way except that no
new step is appended, since no new model call happened.

## Result anatomy

`*GenerateTextResult` reflects the *last* step's text/tool calls/finish
reason at the top level, alongside the full history:

- **`Steps`** (`[]Step`) — one entry per model call in the loop. Each `Step`
  has `Text`, `ReasoningText`, `Sources`, `ToolCalls`, `ToolResults`,
  `FinishReason`, `Usage`, and the raw `*provider.Response`.
- **`Usage`** (`provider.Usage`) — summed across every step, not just the
  last one.
- **`Messages`** (`[]provider.Message`) — the full final conversation,
  including the system/user messages you supplied, every assistant turn,
  and every tool-result message the loop appended. This is what you feed
  back in to continue the conversation (see below).

## Conversation continuation

`Messages` on the result is a complete transcript — append a new turn and
pass it back in as `Messages` on the next call:

```go
first, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "My favorite color is teal. Remember that.",
})
if err != nil {
	log.Fatal(err)
}

// first.Messages is the full conversation so far, including the
// assistant's reply. Append a new user turn and pass it back in as
// Messages to continue the conversation.
messages := append(first.Messages, provider.UserText("What's my favorite color?"))

second, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:    model,
	Messages: messages,
})
```

## Retries and RetryError

Every model call goes through a retry wrapper: `MaxRetries` (default 2) is
the number of retries attempted after the first failure, using the
provider's error to decide retryability (see
[Errors and retries](errors-and-retries.md)). If every attempt fails,
`GenerateText`/`StreamText` return a `*ai.RetryError`:

```go
_, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Hello",
})
if err != nil {
	var retryErr *ai.RetryError
	if errors.As(err, &retryErr) {
		fmt.Printf("gave up after %d attempts: %v\n", retryErr.Attempts, retryErr.LastErr)
		return
	}
	log.Fatal(err)
}
```

`RetryError.Attempts` is the total number of attempts made; `LastErr` is the
error from the final attempt (`RetryError` also implements `Unwrap()`, so
`errors.Is`/`errors.As` against `LastErr`'s chain work directly on the
`*RetryError` too).

## Source of truth

- [`ai/options.go`](../../ai/options.go)
- [`ai/generate_text.go`](../../ai/generate_text.go)
- [`ai/stream_text.go`](../../ai/stream_text.go)
- [`ai/output.go`](../../ai/output.go) (`Output`, `OutputObject`/`OutputArray`/
  `OutputChoice`/`OutputJSON`, `OutputAs`)
- [`ai/approval.go`](../../ai/approval.go), [`ai/runtime_context.go`](../../ai/runtime_context.go)
- [`ai/errors.go`](../../ai/errors.go)
- [`provider/message.go`](../../provider/message.go)

See also: [Structured output](structured-output.md#generateobject-vs-output-modes)
for `GenerateObject` vs `Output` modes; [Reasoning](reasoning.md) for the
unified `Reasoning` call option; [Tools § Approvals for tool execution](tools.md#approvals-for-tool-execution)
and [§ RuntimeContext](tools.md#runtimecontext) for the full approval/
context-passing mechanics; [Streaming § Suspension in streams](streaming.md#suspension-in-streams)
for how suspension surfaces in `StreamText`; [Agents](agents.md) and
[Code Mode](code-mode.md) for two higher-level constructs built on top of
this loop.
