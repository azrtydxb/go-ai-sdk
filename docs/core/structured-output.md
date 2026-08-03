# Structured output

`ai.GenerateObject[T]` and `ai.StreamObject[T]` decode a model's output
directly into a Go value of type `T`, using `T`'s reflected JSON Schema
(the same derivation `NewTool` uses — see [Tools](tools.md#schema-derivation-rules))
to constrain what the model produces.

## Worked example: struct → schema → result

```go
type Recipe struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
	PrepMinutes int      `json:"prep_minutes"`
}

result, err := ai.GenerateObject[Recipe](context.Background(), ai.GenerateObjectOpts{
	Model:             model,
	Prompt:            "Give me a simple pancake recipe.",
	SchemaName:        "recipe",
	SchemaDescription: "A cooking recipe",
})
if err != nil {
	var noObj *ai.NoObjectGeneratedError
	if errors.As(err, &noObj) {
		log.Fatalf("model didn't produce a valid object: %v (raw: %s)", noObj.Cause, noObj.RawText)
	}
	log.Fatal(err)
}

fmt.Println(result.Object.Name)
fmt.Println(result.Object.Ingredients)
fmt.Println(result.Usage.TotalTokens)
```

`GenerateObjectOpts` mirrors the shape of `GenerateTextOpts` for the fields
that apply: `Model` (required), `System`, exactly one of `Prompt`/`Messages`,
`MaxTokens`, `Temperature`, and `MaxRetries` (default 2, same retry/backoff
behavior as `GenerateText`). `SchemaName` defaults to `"output"` when
unset; `SchemaDescription` is optional. `*GenerateObjectResult[T]` gives you
`Object` (the decoded `T`), `RawText` (what was decoded), `Usage`, and
`FinishReason`.

## Native JSON vs tool mode

`GenerateObject`/`StreamObject` pick their wire strategy from
`opts.Model.Capabilities().NativeJSON`:

- **Native JSON mode** (`NativeJSON: true`) — the schema is sent via
  `Call.ResponseFormat`, and the provider constrains its own output to
  match it.
- **Tool mode** (`NativeJSON: false`) — a single tool is injected
  (`Call.Tools`) with the schema as its parameters, and `ToolChoice` is
  forced to that tool (`provider.ToolChoiceTool`). The object is decoded
  from that forced tool call's arguments instead of the response text.

| Provider | `Capabilities().NativeJSON` |
|---|---|
| OpenAI | true |
| Anthropic | false (tool mode) |
| Google | true |
| Vertex AI | true |
| Azure OpenAI | true |
| Amazon Bedrock | false (tool mode) |
| Groq | true |
| xAI | true |
| DeepSeek | true¹ |
| Cerebras | true |
| Together AI | true |
| Fireworks | true |
| Perplexity | true |
| Mistral | true² |
| Cohere | true |

¹ ² footnotes below. (ElevenLabs has no `LanguageModel` — it's a
speech/transcription-only provider, so it doesn't apply here; see
[Media](media.md).)

### ¹ DeepSeek: `JSONObjectOnly`

DeepSeek's `response_format` only accepts `{"type":"json_object"}`, not
OpenAI's schema-bearing `json_schema` shape — sending the latter is
rejected. DeepSeek's preset sets the shared OpenAI-compatible base's
`JSONObjectOnly` option, which sends the bare `json_object` type and drops
the schema from the wire request entirely. Schema conformance for DeepSeek
is therefore enforced only by `GenerateObject`'s own decode step, not by
the provider.

### ² Mistral: schema dropped from `response_format`

Mistral has no `json_schema` mode at all: for `ResponseFormat.Type ==
"json"` it always sends `{"type":"json_object"}` and ignores any `Schema`.
As with DeepSeek, schema conformance for Mistral is enforced by the decode
step in `GenerateObject`/`StreamObject`, not by the wire request.

## NoObjectGeneratedError

Both `GenerateObject` and the completed `StreamObject` return a
`*ai.NoObjectGeneratedError` (with `RawText` and `Cause`) whenever the
model's output couldn't be turned into a valid `T`: JSON decoding failed,
or — in tool mode — the model didn't call the injected tool at all. Check
for it with `errors.As` to inspect the raw text the model actually
produced, which is often useful for debugging prompts.

## StreamObject: partials, Final(), and Close()

`StreamObject[T]` returns an `*ai.ObjectStream[T]`. Its `Partials()` method
is a single-use `iter.Seq[T]`: each yield is a new snapshot of `T`, decoded
from the accumulated JSON seen so far (repaired via a partial-JSON
completer, since the accumulated text is usually not yet valid JSON) once
it successfully unmarshals into `T` *and* differs from the last snapshot
yielded.

```go
stream, err := ai.StreamObject[Recipe](context.Background(), ai.GenerateObjectOpts{
	Model:  model,
	Prompt: "Give me a simple pancake recipe.",
})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

for partial := range stream.Partials() {
	fmt.Printf("partial: %+v\n", partial)
}
if err := stream.Err(); err != nil {
	log.Fatal(err)
}

final, err := stream.Final()
if err != nil {
	log.Fatal(err)
}
fmt.Println("final:", final.Name)
```

- **`Err()`** reflects only a mid-stream provider error; a `*RetryError`
  from a failure to *start* the stream is returned by `StreamObject`
  itself, not surfaced through `Err()`.
- **`Final()`** returns the last valid decode of the *complete* accumulated
  stream text (fences stripped, but not partial-repaired — the finished
  stream is expected to be complete JSON). It's only meaningful after
  `Partials()` has been iterated to completion: calling it before
  `Partials()` was ever ranged over, or after abandoning iteration early
  (breaking out of the `for range` before the stream ends), returns a
  `*NoObjectGeneratedError` rather than silently reporting a zero-value
  `T` as success.
- **`Close()`** is idempotent and safe to call at any point — before
  consuming the stream, mid-iteration, or after `Partials()` already closed
  it itself (which it does both on natural completion and on early
  abandonment). Calling `defer stream.Close()` right after `StreamObject`
  returns is always safe.

## GenerateObject vs Output modes

`ai.GenerateObject[T]`/`ai.StreamObject[T]` and `GenerateTextOpts.Output`
(see [Generating text § Output modes](generating-text.md#output-modes)) both
decode a model's response into a typed Go value using the native-JSON/
tool-mode strategy above — they share the same fallback logic and the same
`*ai.NoObjectGeneratedError` failure mode. Which to reach for:

- **`GenerateObject[T]`/`StreamObject[T]`** — the call's *only* output is a
  structured object; no text response, no tool calls of your own, and (with
  `StreamObject`) you want incremental `Partials()` as the object accumulates.
- **`GenerateTextOpts.Output`** — you're already using `GenerateText`
  (possibly with your own `Tools` and a multi-step tool loop) and want the
  *final* step's text decoded into a Go value at the end, without a second
  call. It adds four shapes `GenerateObject` doesn't have on its own
  (`OutputArray[T]`, `OutputChoice`, `OutputJSON`, and reusing whatever tool
  loop already ran) — but `Output` is `GenerateText`-only: `StreamText`
  returns `ai.ErrOutputWithStreamText` immediately if `Output` is set, so use
  `StreamObject[T]` when you need streaming.

Both fall back to a forced single tool call on the same condition
(`Capabilities().NativeJSON == false`); `Output`'s tool-mode fallback
additionally requires `GenerateTextOpts.Tools` to be empty, since the
injected output tool and your own tools can't both be forced-or-offered on a
model with no other way to constrain JSON output —
`ai.ErrOutputRequiresJSONOrNoTools` otherwise.

## Source of truth

- [`ai/generate_object.go`](../../ai/generate_object.go)
- [`ai/stream_object.go`](../../ai/stream_object.go)
- [`ai/errors.go`](../../ai/errors.go)
- [`provider/model.go`](../../provider/model.go) (`Capabilities`)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go) (`JSONObjectOnly`)
- [`providers/deepseek/deepseek.go`](../../providers/deepseek/deepseek.go), [`providers/mistral/wire.go`](../../providers/mistral/wire.go)
