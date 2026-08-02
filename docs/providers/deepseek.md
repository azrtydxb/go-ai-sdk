# DeepSeek

DeepSeek's API is OpenAI-chat-completions compatible; `providers/deepseek`
is a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/deepseek"

// APIKey defaults to os.Getenv("DEEPSEEK_API_KEY"); BaseURL defaults to
// "https://api.deepseek.com/v1". Auth is sent as "Authorization: Bearer <key>".
model := deepseek.New(
	deepseek.WithAPIKey("sk-..."),   // optional, overrides DEEPSEEK_API_KEY
	deepseek.WithBaseURL("..."),     // optional, overrides the default base URL
).Model("deepseek-chat")
```

`deepseek.New` also accepts `deepseek.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `deepseek.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`, wired
  through the shared chat-completions request shape.
- **Reasoning (`reasoning_content`)** — DeepSeek-R1-style models report
  their chain of thought via `reasoning_content`, surfaced as
  `provider.ReasoningPart` (non-streamed) or a run of
  `provider.ReasoningDelta`s (streamed). See
  [Reasoning](../core/reasoning.md#deepseek-reasoning_content).
- **Structured JSON output** — supported, but constrained; see Quirks below.

Not wired for this preset: embeddings, image generation, speech, or
transcription — `deepseek.Provider` only exposes `Model`, not
`EmbeddingModel`.

## Quirks and notes

- **`response_format` is `json_object`-only.** DeepSeek's API rejects
  `json_schema` response formats. `providers/deepseek/deepseek.go` sets
  `openaicompat.Config.JSONObjectOnly: true` for exactly this reason:

  ```go
  // DeepSeek's response_format only accepts json_object, not
  // json_schema; it rejects requests that send the latter.
  JSONObjectOnly: true,
  ```

  With `JSONObjectOnly` set, `convertResponseFormat` in
  `internal/openaicompat/wire.go` always sends
  `{"type":"json_object"}` on the wire and drops any `Schema` you passed in
  `provider.ResponseFormat`, even when one is provided — schema conformance
  for DeepSeek is enforced by the `ai` core's decode step against the model's
  free-form JSON output, not by the wire request itself. Practically: ask
  for JSON in your prompt (DeepSeek's `json_object` mode requires the word
  "json" to appear somewhere in the messages) rather than relying on a
  schema being forwarded.

- **`max_tokens`, not `max_completion_tokens`.** DeepSeek documents the
  older `max_tokens` field name and silently ignores OpenAI's current
  `max_completion_tokens`, so the preset sets
  `openaicompat.Config.MaxTokensParam: "max_tokens"`.

- **`reasoning_content` is not replayed on later turns.** When a
  `provider.ReasoningPart` from a prior assistant turn is sent back as
  request history, `internal/openaicompat/wire.go`'s `convertMessages`
  drops it — DeepSeek-R1 and compatible APIs expect `reasoning_content` to
  be absent from prior turns in the request.

## ProviderOptions

Raw wire keys are merged verbatim into the chat-completions request body
(see [Provider options](../core/provider-options.md)). `temperature` and
`logprobs` below are the two keys directly exercised by
`internal/openaicompat/provideroptions_test.go`
(`TestChatProviderOptionsOverridesAndPassthrough`) — `temperature`
overrides `provider.Call.Temperature`, and `logprobs` is a novel
passthrough key with no dedicated `provider.Call` field:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Explain the CAP theorem in one sentence.",
	ProviderOptions: map[string]any{
		"deepseek": map[string]any{
			"temperature": 0.9,
			"logprobs":    true,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.

## Source of truth

- [`providers/deepseek/deepseek.go`](../../providers/deepseek/deepseek.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config.JSONObjectOnly`, `Config.MaxTokensParam`)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`convertResponseFormat`, `ReasoningContent` handling in `convertMessages`
  and `convertResponse`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
- [`docs/core/reasoning.md`](../core/reasoning.md#deepseek-reasoning_content)
