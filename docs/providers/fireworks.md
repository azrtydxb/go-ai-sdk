# Fireworks

Fireworks' API is OpenAI-chat-completions compatible; `providers/fireworks`
is a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/fireworks"

// APIKey defaults to os.Getenv("FIREWORKS_API_KEY"); BaseURL defaults to
// "https://api.fireworks.ai/inference/v1". Auth is sent as
// "Authorization: Bearer <key>".
p := fireworks.New(
	fireworks.WithAPIKey("..."),  // optional, overrides FIREWORKS_API_KEY
	fireworks.WithBaseURL("..."), // optional, overrides the default base URL
)
model := p.Model("accounts/fireworks/models/llama-v3p1-70b-instruct")
```

`fireworks.New` also accepts `fireworks.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `p.Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `p.EmbeddingModel(id)` returns a `provider.EmbeddingModel`
  with `MaxBatchSize() == 100` (`EmbedBatch: 100` in the preset's `Config`):

  ```go
  embedder := p.EmbeddingModel("nomic-ai/nomic-embed-text-v1.5")
  resp, err := embedder.Embed(context.Background(), []string{"hello world"})
  ```

Not wired for this preset: image generation, speech, or transcription —
`fireworks.Provider` exposes only `Model` and `EmbeddingModel`.

## Quirks and notes

- **`max_tokens`, not `max_completion_tokens`.** Fireworks documents the
  older `max_tokens` field name and silently ignores OpenAI's current
  `max_completion_tokens`, so the preset sets
  `openaicompat.Config.MaxTokensParam: "max_tokens"`.
- The default base URL has an `/inference/v1` suffix, not just `/v1` —
  `https://api.fireworks.ai/inference/v1` — distinct from the
  `https://api.<provider>.xyz/v1` / `.ai/v1` shape used by Together and
  Cerebras. Pass a full replacement (not a partial path) to
  `fireworks.WithBaseURL` if you need to override it.
- No `JSONObjectOnly` restriction — unlike DeepSeek, Fireworks accepts full
  `json_schema` response formats.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)). `temperature` and
`logprobs` below are the two keys directly exercised by
`internal/openaicompat/provideroptions_test.go`
(`TestChatProviderOptionsOverridesAndPassthrough`) — `temperature`
overrides `provider.Call.Temperature`, and `logprobs` is a novel
passthrough key with no dedicated `provider.Call` field:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Name three moons of Jupiter.",
	ProviderOptions: map[string]any{
		"fireworks": map[string]any{
			"temperature": 0.9,
			"logprobs":    true,
		},
	},
})
```

Embedding calls accept the same namespaced-map convention via
`provider.EmbeddingCall.ProviderOptions` (verified against
`internal/openaicompat/provideroptions_test.go`'s
`TestEmbeddingProviderOptionsOverridesAndPassthrough`, which exercises
`"model"` and `"encoding_format"` as override/passthrough keys):

```go
optioned := embedder.(provider.EmbeddingModelWithOptions)
resp, err := optioned.EmbedCall(context.Background(), provider.EmbeddingCall{
	Values: []string{"hello world"},
	ProviderOptions: map[string]any{
		"fireworks": map[string]any{
			"encoding_format": "base64",
		},
	},
})
```

## Source of truth

- [`providers/fireworks/fireworks.go`](../../providers/fireworks/fireworks.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
