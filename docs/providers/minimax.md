# MiniMax

MiniMax's API is OpenAI-chat-completions compatible; `providers/minimax` is
a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/minimax"

// APIKey defaults to os.Getenv("MINIMAX_API_KEY"); BaseURL defaults to
// "https://api.minimax.io/v1". Auth is sent as "Authorization: Bearer <key>".
model := minimax.New(
	minimax.WithAPIKey("..."), // optional, overrides MINIMAX_API_KEY
	minimax.WithBaseURL("..."), // optional, overrides the default base URL
).Model("abab6.5s-chat")
```

`minimax.New` also accepts `minimax.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `minimax.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.

Not wired for this preset: `minimax.Provider` only exposes `Model` — no
`EmbeddingModel`, image, speech, or transcription support.

## Quirks and notes

- **`max_tokens`, not `max_completion_tokens`.** MiniMax documents
  `max_tokens`, so this preset sets `Config.MaxTokensParam: "max_tokens"` —
  same override as Together/Fireworks/DeepSeek/Qwen, and unlike the OpenAI
  default (`internal/openaicompat/wire.go`'s `defaultMaxTokensParam`).
- No other provider-specific `Config` quirks — see
  [`providers/minimax/minimax.go`](../../providers/minimax/minimax.go) for
  the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://api.minimax.io/v1` endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the chat-completions request body
(see [Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"minimax": map[string]any{
			"temperature": 0.9,
			"top_p":       0.95,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.

## Source of truth

- [`providers/minimax/minimax.go`](../../providers/minimax/minimax.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
