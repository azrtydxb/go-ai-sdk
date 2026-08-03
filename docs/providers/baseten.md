# Baseten

Baseten's Model APIs expose an OpenAI-chat-completions compatible interface;
`providers/baseten` is a thin preset over the shared `internal/openaicompat`
base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/baseten"

// APIKey defaults to os.Getenv("BASETEN_API_KEY"); BaseURL defaults to
// "https://inference.baseten.co/v1". Auth is sent as
// "Authorization: Bearer <key>".
provider := baseten.New(
	baseten.WithAPIKey("..."), // optional, overrides BASETEN_API_KEY
	baseten.WithBaseURL("..."), // optional, overrides the default base URL
)
model := provider.Model("deepseek-ai/DeepSeek-V3")
embedder := provider.EmbeddingModel("BAAI/bge-large-en-v1.5")
```

`baseten.New` also accepts `baseten.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `baseten.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `baseten.New().EmbeddingModel(id)`,
  `MaxBatchSize() == 1` (`openaicompat.Config.EmbedBatch: 1`) — Baseten's
  Model APIs front arbitrary deployed models with no shared batch-size
  guarantee, so this preset is conservative and issues one request per
  value (see [Embeddings § MaxBatchSize by provider](../core/embeddings.md#maxbatchsize-by-provider)).

Not wired for this preset: no image, speech, or transcription support.

## Quirks and notes

- No `Config.MaxTokensParam` override — Baseten follows OpenAI's current
  `max_completion_tokens` field name, so
  `internal/openaicompat/wire.go`'s `defaultMaxTokensParam` applies
  unchanged.
- No other provider-specific `Config` quirks — see
  [`providers/baseten/baseten.go`](../../providers/baseten/baseten.go) for
  the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://inference.baseten.co/v1`
endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"baseten": map[string]any{
			"temperature": 0.9,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.
The same convention applies to embedding calls via
`ProviderOptions["baseten"]`.

## Source of truth

- [`providers/baseten/baseten.go`](../../providers/baseten/baseten.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
