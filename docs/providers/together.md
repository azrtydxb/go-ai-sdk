# Together

Together's API is OpenAI-chat-completions compatible; `providers/together`
is a thin preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/together"

// APIKey defaults to os.Getenv("TOGETHER_AI_API_KEY"); BaseURL defaults to
// "https://api.together.xyz/v1". Auth is sent as "Authorization: Bearer <key>".
p := together.New(
	together.WithAPIKey("..."),   // optional, overrides TOGETHER_AI_API_KEY
	together.WithBaseURL("..."),  // optional, overrides the default base URL
)
model := p.Model("meta-llama/Llama-3.3-70B-Instruct-Turbo")
```

`together.New` also accepts `together.WithHTTPClient(*http.Client)` to
override the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `p.Model(id)`, used with
  `ai.GenerateText` / `ai.StreamText`.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Native JSON mode** — `openaicompat.Config.NativeJSON: true`.
- **Embeddings** — `p.EmbeddingModel(id)` returns a `provider.EmbeddingModel`
  with `MaxBatchSize() == 100` (`EmbedBatch: 100` in the preset's `Config`):

  ```go
  embedder := p.EmbeddingModel("togethercomputer/m2-bert-80M-8k-retrieval")
  resp, err := embedder.Embed(context.Background(), []string{"hello world"})
  ```

Not wired for this preset: image generation, speech, or transcription —
`together.Provider` exposes only `Model` and `EmbeddingModel`.

## Quirks and notes

- **`max_tokens`, not `max_completion_tokens`.** Together documents the
  older `max_tokens` field name and silently ignores OpenAI's current
  `max_completion_tokens`, so the preset sets
  `openaicompat.Config.MaxTokensParam: "max_tokens"`.
- Embedding requests batch through `internal/openaicompat/embedding.go`;
  `MaxBatchSize()` reflects the `EmbedBatch: 100` set in
  `providers/together/together.go`'s `EmbeddingModel` constructor.
- No `JSONObjectOnly` restriction — unlike DeepSeek, Together accepts full
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
	Prompt: "Summarize the plot of Hamlet in one sentence.",
	ProviderOptions: map[string]any{
		"together": map[string]any{
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
		"together": map[string]any{
			"encoding_format": "base64",
		},
	},
})
```

## Source of truth

- [`providers/together/together.go`](../../providers/together/together.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
