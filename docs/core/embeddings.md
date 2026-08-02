# Embeddings

`ai.Embed` and `ai.EmbedMany` turn text into vectors using any
`provider.EmbeddingModel`. `ai.CosineSimilarity` compares two vectors.

## Embed

```go
model := openai.New().EmbeddingModel("text-embedding-3-small")

result, err := ai.Embed(context.Background(), ai.EmbedOpts{
	Model: model,
	Value: "hello world",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Embedding) // []float64
fmt.Println(result.Usage.TotalTokens)
```

`EmbedOpts`:

- **`Model`** (`provider.EmbeddingModel`, required) — a `nil` model returns
  `ai.ErrModelRequired`.
- **`Value`** (`string`, required) — the text to embed.
- **`MaxRetries`** (`*int`, default `2`) — same retry wrapper as
  `GenerateText` (see [Errors and retries](errors-and-retries.md)); on
  exhaustion `Embed` returns a `*ai.RetryError`.
- **`ProviderOptions`** (`map[string]any`) — only takes effect if `Model`
  also implements `provider.EmbeddingModelWithOptions` (see below);
  otherwise silently ignored.

`Embed` wraps a single value into a one-element batch and returns an error
(`fmt.Errorf`, not one of the typed errors) if the model returns a different
number of embeddings than requested — which should never happen with a
correctly implemented provider.

## EmbedMany and batching

```go
result, err := ai.EmbedMany(context.Background(), ai.EmbedManyOpts{
	Model:  model,
	Values: []string{"first chunk", "second chunk", "third chunk"},
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(len(result.Embeddings)) // index-aligned with Values
fmt.Println(result.Usage.TotalTokens)
```

`EmbedMany` splits `Values` into chunks of at most `model.MaxBatchSize()`,
calls the model once per chunk (each call independently retried), and
reassembles the results in order. `Usage` is summed across every chunk
call, field by field (`InputTokens`, `OutputTokens`, `TotalTokens`,
`CachedInputTokens`, `ReasoningTokens`). An empty `Values` returns an empty
result (`Embeddings: [][]float64{}`) without calling the model at all. If a
chunk call returns a different number of embeddings than values sent for
that chunk, `EmbedMany` returns an error and the whole call fails — there is
no partial result.

### MaxBatchSize by provider

Every embedding-capable provider implements `MaxBatchSize()`; `EmbedMany`
reads it directly off `opts.Model` (a batch size of zero or less is treated
as 1, to avoid an infinite loop):

| Provider | `MaxBatchSize()` |
|---|---|
| OpenAI | 2048 |
| Azure OpenAI | 2048 |
| Google (Gemini) | 100 |
| Together AI | 100 |
| Fireworks | 100 |
| Vertex AI | 250 |
| Cohere | 96 |
| Mistral | 32 |
| Amazon Bedrock | 1 |

Anthropic has no embeddings API and does not implement
`provider.EmbeddingModel` at all — matching the TS AI SDK's behavior.
Bedrock's Titan/Cohere embedding models have no batched request shape, so
its `MaxBatchSize()` is 1: `EmbedMany` still works against it, it just
issues one call per value.

## EmbeddingModelWithOptions

`ProviderOptions` on `EmbedOpts`/`EmbedManyOpts` only has an effect when the
model also implements the optional extension interface:

```go
type EmbeddingModelWithOptions interface {
	EmbeddingModel
	EmbedCall(ctx context.Context, call EmbeddingCall) (*EmbeddingResponse, error)
}
```

This is a separate interface (rather than changing `Embed`'s signature) so
that providers with nothing to say about provider options don't need an
unused parameter. Among the providers in this SDK, only Vertex AI
implements it today; for every other embedding provider, setting
`ProviderOptions` compiles fine but has no effect on the request sent.

```go
vm := vertex.New(
	vertex.WithProject("my-project"),
	vertex.WithLocation("us-central1"),
).EmbeddingModel("text-embedding-004")

if _, ok := vm.(provider.EmbeddingModelWithOptions); ok {
	// vm accepts ProviderOptions on Embed/EmbedMany calls.
}
```

## CosineSimilarity

```go
sim, err := ai.CosineSimilarity([]float64{1, 0, 0}, []float64{0.5, 0.5, 0})
if err != nil {
	log.Fatal(err)
}
fmt.Println(sim) // 0.7071067811865475
```

`CosineSimilarity(a, b []float64) (float64, error)` computes
`dot(a, b) / (||a|| * ||b||)`. It returns an error if `a` and `b` have
different lengths, or if either vector has zero magnitude (cosine
similarity is undefined for a zero vector) — it never panics or silently
returns `0` for a malformed input.

## Source of truth

- [`ai/embed.go`](../../ai/embed.go)
- [`ai/similarity.go`](../../ai/similarity.go)
- [`provider/model.go`](../../provider/model.go)
- [`providers/vertex/embedding.go`](../../providers/vertex/embedding.go)

See also: [Generating text](generating-text.md) for the shared retry
wrapper, [Provider options](provider-options.md) for the general
`ProviderOptions` convention.
