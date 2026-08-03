# Moonshot

Moonshot's API is OpenAI-chat-completions compatible; `providers/moonshot` is
a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/moonshot"

// APIKey defaults to os.Getenv("MOONSHOT_API_KEY"); BaseURL defaults to
// "https://api.moonshot.ai/v1". Auth is sent as "Authorization: Bearer <key>".
model := moonshot.New(
	moonshot.WithAPIKey("sk-..."), // optional, overrides MOONSHOT_API_KEY
	moonshot.WithBaseURL("..."),   // optional, overrides the default base URL
).Model("moonshot-v1-8k")
```

`moonshot.New` also accepts `moonshot.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `moonshot.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`, so
  `provider.Capabilities().NativeJSON` reports `true` for this preset.

Not wired for this preset: `moonshot.Provider` only exposes `Model` — no
`EmbeddingModel`, image, speech, or transcription support.

## Quirks and notes

- No `Config.MaxTokensParam` override — Moonshot follows OpenAI's current
  `max_completion_tokens` field name, so
  `internal/openaicompat/wire.go`'s `defaultMaxTokensParam` applies
  unchanged.
- No provider-specific `Config` quirks beyond `NativeJSON: true` — see
  [`providers/moonshot/moonshot.go`](../../providers/moonshot/moonshot.go)
  for the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://api.moonshot.ai/v1` endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the chat-completions request body
(see [Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"moonshot": map[string]any{
			"temperature": 0.9,
			"logprobs":    true,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.

## Source of truth

- [`providers/moonshot/moonshot.go`](../../providers/moonshot/moonshot.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
