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
- **`MaxRetries`** (`*int`, default `2`) — the number of retries around each
  model call (see [Retries](#retries-and-retryerror) below).

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
- **`StopWhen`** (`func(steps []Step) bool`, optional) — evaluated after each
  step that requested tool calls (a step with no tool calls always ends the
  loop naturally, without consulting `StopWhen`). Returning `true` stops the
  loop.

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
steps have completed.

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
- [`ai/errors.go`](../../ai/errors.go)
- [`provider/message.go`](../../provider/message.go)
