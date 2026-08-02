# Azure OpenAI

`providers/azure` talks to Azure OpenAI's OpenAI-compatible "v1" surface —
a preset over the shared `internal/openaicompat` base with two real
differences from the plain OpenAI preset: authentication goes over the
`api-key` header instead of `Authorization: Bearer`, and model IDs are
Azure **deployment names**, not OpenAI model names.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/azure"

p := azure.New(
	azure.WithAPIKey("azure-key"),          // defaults to os.Getenv("AZURE_API_KEY")
	azure.WithResourceName("my-resource"),  // defaults to os.Getenv("AZURE_RESOURCE_NAME")
	// azure.WithBaseURL(...) overrides the derived URL entirely, taking
	// precedence over WithResourceName / AZURE_RESOURCE_NAME.
)

model := p.Model("my-gpt4-deployment") // an Azure deployment name, not "gpt-4o"
```

There is no fixed default base URL like the other three presets. It's
derived from the resource name as
`https://{resource}.openai.azure.com/openai/v1`, or taken verbatim from
`WithBaseURL` if set. If neither a resource name nor a base URL is
configured, the returned model doesn't error at construction time — calls
to `Generate`/`Stream`/`Embed` fail with `"azure: base URL not
configured"` at request time.

## Supported capabilities

- **Text generation & streaming** — `p.Model(deploymentName)`.
- **Tool calling** — same `Model(id)`, wired through the shared
  `openaicompat` chat-completions layer.
- **Structured output** — `NativeJSON: true` in the underlying config, so
  `json_schema` response formats are used.
- **Embeddings** — `p.EmbeddingModel(deploymentName)`; batches up to 2048
  inputs per call.

No `ImageModel`, `SpeechModel`, or `TranscriptionModel` constructor is
exposed by this preset — only `Model` and `EmbeddingModel`.

## Quirks and notes

- **Deployment names, not model names.** The `id` passed to `Model`/
  `EmbeddingModel` is whatever you named the deployment in the Azure
  portal — it does not need to match (and usually doesn't look like) an
  OpenAI model ID such as `"gpt-4o"`.
- **`api-key` header, not `Authorization: Bearer`.** `Config.APIKeyHeader`
  is set to `"api-key"` for both `Model` and `EmbeddingModel`, so the SDK
  sends `api-key: <your-key>` and omits `Authorization` entirely.
- **No fixed default base URL.** Unlike OpenAI/Groq/xAI, Azure requires
  either `AZURE_RESOURCE_NAME`/`WithResourceName` or an explicit
  `WithBaseURL` — there's no bare `New()` that "just works" against a
  public endpoint the way the other three presets do.
- `WithBaseURL` always wins over `WithResourceName` / the
  `AZURE_RESOURCE_NAME` env var when both are set.

## ProviderOptions

Entries under `ProviderOptions["azure"]` merge into the request body the
same way as every other `openaicompat` preset — raw wire key names,
shallow-merged after the SDK builds its own request:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("my-gpt4-deployment"),
	Prompt: "Summarize this incident report.",
	ProviderOptions: map[string]any{
		"azure": map[string]any{
			"temperature": 0.2, // overrides Call.Temperature
			"logprobs":    true, // passthrough key
		},
	},
})
```

(These are the same wire keys `openaicompat`'s shared request-shape tests
exercise for `temperature` overrides and passthrough keys like
`logprobs` — Azure's preset shares the exact same merge point, just keyed
under `"azure"` instead of `"openai"`.)

## Source of truth

- [`providers/azure/azure.go`](../../providers/azure/azure.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config.APIKeyHeader`, `setAuthHeader`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
  (`"base URL not configured"` error)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`)
- [`providers/azure/azure_test.go`](../../providers/azure/azure_test.go)
  (`TestDefaults`, `TestAPIKeyHeaderSent`, `TestNoBaseURLErrors`,
  `TestResourceNameThenBaseURL`)
