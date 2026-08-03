# DeepInfra

DeepInfra's API is OpenAI-chat-completions compatible; `providers/deepinfra`
is a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/deepinfra"

// APIKey defaults to os.Getenv("DEEPINFRA_API_KEY"); BaseURL defaults to
// "https://api.deepinfra.com/v1/openai". Auth is sent as
// "Authorization: Bearer <key>".
provider := deepinfra.New(
	deepinfra.WithAPIKey("..."), // optional, overrides DEEPINFRA_API_KEY
	deepinfra.WithBaseURL("..."), // optional, overrides the default base URL
)
model := provider.Model("meta-llama/Meta-Llama-3.1-8B-Instruct")
embedder := provider.EmbeddingModel("BAAI/bge-base-en-v1.5")
```

`deepinfra.New` also accepts `deepinfra.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `deepinfra.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `deepinfra.New().EmbeddingModel(id)`,
  `MaxBatchSize() == 1024` (`openaicompat.Config.EmbedBatch: 1024`) — the
  largest batch size of any preset in this SDK, reflecting DeepInfra's
  documented embeddings batch limit.

Not wired for this preset: no image, speech, or transcription support.

## Quirks and notes

- No `Config.MaxTokensParam` override — DeepInfra follows OpenAI's current
  `max_completion_tokens` field name, so
  `internal/openaicompat/wire.go`'s `defaultMaxTokensParam` applies
  unchanged.
- **Base URL includes `/v1/openai`**, not just `/v1` — DeepInfra exposes its
  OpenAI-compatible surface at a nested path
  (`https://api.deepinfra.com/v1/openai`), unlike most other presets.
- No other provider-specific `Config` quirks — see
  [`providers/deepinfra/deepinfra.go`](../../providers/deepinfra/deepinfra.go)
  for the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live DeepInfra endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"deepinfra": map[string]any{
			"temperature": 0.9,
			"top_p":       0.95,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.
The same convention applies to embedding calls via
`ProviderOptions["deepinfra"]`.

## Source of truth

- [`providers/deepinfra/deepinfra.go`](../../providers/deepinfra/deepinfra.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
