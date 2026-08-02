# Cerebras

Cerebras's API is OpenAI-chat-completions compatible; `providers/cerebras`
is a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/cerebras"

// APIKey defaults to os.Getenv("CEREBRAS_API_KEY"); BaseURL defaults to
// "https://api.cerebras.ai/v1". Auth is sent as "Authorization: Bearer <key>".
model := cerebras.New(
	cerebras.WithAPIKey("csk-..."),  // optional, overrides CEREBRAS_API_KEY
	cerebras.WithBaseURL("..."),     // optional, overrides the default base URL
).Model("llama3.1-8b")
```

`cerebras.New` also accepts `cerebras.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `cerebras.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`, so
  `provider.Capabilities().NativeJSON` reports `true` for this preset; full
  `json_schema` structured output is sent as-is (no `JSONObjectOnly`
  restriction, unlike DeepSeek).

Not wired for this preset: `cerebras.Provider` only exposes `Model` — no
`EmbeddingModel`, image, speech, or transcription support.

## Quirks and notes

- Cerebras uses OpenAI's current `max_completion_tokens` field name — the
  preset does **not** set `Config.MaxTokensParam`, so
  `internal/openaicompat/wire.go`'s `defaultMaxTokensParam` applies
  unchanged. This differs from DeepSeek/Together/Fireworks, which all
  override it to `max_tokens`.
- No provider-specific `Config` quirks beyond `NativeJSON: true` — see
  [`providers/cerebras/cerebras.go`](../../providers/cerebras/cerebras.go)
  for the full preset.

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
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"cerebras": map[string]any{
			"temperature": 0.9,
			"logprobs":    true,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.

## Source of truth

- [`providers/cerebras/cerebras.go`](../../providers/cerebras/cerebras.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
