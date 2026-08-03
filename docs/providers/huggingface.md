# Hugging Face

Hugging Face's Inference Router exposes an OpenAI-chat-completions
compatible API; `providers/huggingface` is a thin preset over the shared
`internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/huggingface"

// APIKey defaults to os.Getenv("HF_TOKEN"); BaseURL defaults to
// "https://router.huggingface.co/v1". Auth is sent as
// "Authorization: Bearer <key>".
model := huggingface.New(
	huggingface.WithAPIKey("hf_..."), // optional, overrides HF_TOKEN
	huggingface.WithBaseURL("..."),   // optional, overrides the default base URL
).Model("meta-llama/Meta-Llama-3-8B-Instruct:together")
```

`huggingface.New` also accepts `huggingface.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `huggingface.New().Model(id)`, used
  with `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.

Not wired for this preset: `huggingface.Provider` only exposes `Model` — no
`EmbeddingModel`, image, speech, or transcription support.

## Quirks and notes

- **`NativeJSON: false`** — unlike most `openaicompat` presets. The Hugging
  Face router fans a single API surface out to many different underlying
  inference providers with inconsistent native-JSON/schema support, so this
  preset is deliberately conservative and does **not** advertise
  `Capabilities().NativeJSON` as `true`; `ai.GenerateObject` falls back to
  tool-mode structured output for this provider (see
  [Structured output](../core/structured-output.md)) rather than risking a
  silent failure against a backend that doesn't honor `response_format`.
- **`max_tokens`, not `max_completion_tokens`.** The router documents
  `max_tokens`, so this preset sets `Config.MaxTokensParam: "max_tokens"` —
  same override as Together/Fireworks/DeepSeek/Qwen/MiniMax.
- **Model IDs can carry a provider suffix**, e.g.
  `"meta-llama/Meta-Llama-3-8B-Instruct:together"` selects a specific
  underlying inference provider behind the router; the `id` passed to
  `Model` is sent verbatim as the wire `model` field with no parsing or
  validation on this SDK's side.
- No other provider-specific `Config` quirks — see
  [`providers/huggingface/huggingface.go`](../../providers/huggingface/huggingface.go)
  for the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://router.huggingface.co/v1`
endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the chat-completions request body
(see [Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"huggingface": map[string]any{
			"temperature": 0.9,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.

## Source of truth

- [`providers/huggingface/huggingface.go`](../../providers/huggingface/huggingface.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
