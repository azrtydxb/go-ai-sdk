# NVIDIA NIM

NVIDIA's NIM API endpoints are OpenAI-chat-completions compatible;
`providers/nvidia` is a thin preset over the shared `internal/openaicompat`
base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/nvidia"

// APIKey defaults to os.Getenv("NVIDIA_API_KEY"); BaseURL defaults to
// "https://integrate.api.nvidia.com/v1". Auth is sent as
// "Authorization: Bearer <key>".
provider := nvidia.New(
	nvidia.WithAPIKey("nvapi-..."), // optional, overrides NVIDIA_API_KEY
	nvidia.WithBaseURL("..."),      // optional, overrides the default base URL
)
model := provider.Model("meta/llama-3.1-8b-instruct")
embedder := provider.EmbeddingModel("nvidia/nv-embedqa-e5-v5")
```

`nvidia.New` also accepts `nvidia.WithHTTPClient(*http.Client)` to override
the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `nvidia.New().Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `nvidia.New().EmbeddingModel(id)`,
  `MaxBatchSize() == 1` (`openaicompat.Config.EmbedBatch: 1`) — a
  conservative default since batch limits vary across NIM's catalog of
  hosted embedding models.

Not wired for this preset: no image, speech, or transcription support.

## Quirks and notes

- No `Config.MaxTokensParam` override — NIM's chat endpoint follows OpenAI's
  current `max_completion_tokens` field name, so
  `internal/openaicompat/wire.go`'s `defaultMaxTokensParam` applies
  unchanged.
- No other provider-specific `Config` quirks — see
  [`providers/nvidia/nvidia.go`](../../providers/nvidia/nvidia.go) for the
  full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://integrate.api.nvidia.com/v1`
endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"nvidia": map[string]any{
			"temperature": 0.9,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.
The same convention applies to embedding calls via
`ProviderOptions["nvidia"]`.

## Source of truth

- [`providers/nvidia/nvidia.go`](../../providers/nvidia/nvidia.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`defaultMaxTokensParam`, `applyProviderOptions`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
