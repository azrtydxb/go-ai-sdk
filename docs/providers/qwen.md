# Qwen

Alibaba's DashScope OpenAI-compatible mode is OpenAI-chat-completions
compatible; `providers/qwen` is a thin preset over the shared
`internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/qwen"

// APIKey defaults to os.Getenv("DASHSCOPE_API_KEY"); BaseURL defaults to
// "https://dashscope-intl.aliyuncs.com/compatible-mode/v1". Auth is sent as
// "Authorization: Bearer <key>".
provider := qwen.New(
	qwen.WithAPIKey("sk-..."), // optional, overrides DASHSCOPE_API_KEY
	qwen.WithBaseURL("..."),   // optional, overrides the default base URL
)
model := provider.Model("qwen-plus")
embedder := provider.EmbeddingModel("text-embedding-v3")
```

`qwen.New` also accepts `qwen.WithHTTPClient(*http.Client)` to override the
client used for requests.

## Supported capabilities

- **Text generation and streaming** — `qwen.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `qwen.New().EmbeddingModel(id)` (e.g.
  `"text-embedding-v3"`), `MaxBatchSize() == 10`
  (`openaicompat.Config.EmbedBatch: 10`).

Not wired for this preset: no image, speech, or transcription support.

## Quirks and notes

- **`max_tokens`, not `max_completion_tokens`.** DashScope's
  OpenAI-compatible mode documents `max_tokens`, so this preset sets
  `Config.MaxTokensParam: "max_tokens"` — same override as Together/
  Fireworks/DeepSeek/MiniMax, and unlike the OpenAI default
  (`internal/openaicompat/wire.go`'s `defaultMaxTokensParam`).
- **`EmbedBatch: 10`** — a conservative batch size for DashScope's embedding
  endpoint; see [Embeddings § MaxBatchSize by provider](../core/embeddings.md#maxbatchsize-by-provider).
- No other provider-specific `Config` quirks — see
  [`providers/qwen/qwen.go`](../../providers/qwen/qwen.go) for the full
  preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live DashScope endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"qwen": map[string]any{
			"temperature": 0.9,
			"top_k":       20,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.
The same convention applies to embedding calls via
`ProviderOptions["qwen"]`, merged into the embeddings request body.

## Source of truth

- [`providers/qwen/qwen.go`](../../providers/qwen/qwen.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
