# Embeddings

`ai.Embed` and `ai.EmbedMany` turn text into vectors using any
`provider.EmbeddingModel`. `ai.CosineSimilarity` compares two vectors.
`ai.Rerank` ranks documents by relevance using a `provider.RerankingModel`
(see [Reranking](#reranking) below).

## Embed

```go
model := openai.New().EmbeddingModel("text-embedding-3-small")

result, err := ai.Embed(context.Background(), ai.EmbedOpts{
	Model: model,
	Value: "hello world",
	OnEmbedStart: func(values []string) {
		fmt.Println("embedding started for", len(values), "value(s)")
	},
	OnEmbedEnd: func(resp *provider.EmbeddingResponse, err error) {
		fmt.Println("embedding finished, err:", err)
	},
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
- **`OnEmbedStart`/`OnEmbedEnd`** — fire once around the (retried) provider
  call: `OnEmbedStart` before the first attempt, `OnEmbedEnd` after the
  final one. `OnEmbedEnd`'s `err`, when non-nil, is the SAME error `Embed`
  itself returns (retry exhaustion already translated to `*ai.RetryError`);
  `resp` is `nil` on error. `EmbedManyOpts` has the same pair, firing once
  per batch (see below).

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

`EmbedManyOpts.OnEmbedStart`/`OnEmbedEnd` fire once **per batch**, in batch
order — same start-before-first-attempt/end-after-final-attempt semantics
and error-translation guarantee as `EmbedOpts`'s pair.

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

## Reranking

`ai.Rerank` ranks `Documents` by relevance to `Query` using any
`provider.RerankingModel` — useful for narrowing a larger embedding-search
candidate set down to the few most relevant results before feeding them to
a model.

```go
model := cohere.New().RerankingModel("rerank-v3.5")

result, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model: model,
	Query: "What's the capital of France?",
	Documents: []string{
		"Paris is the capital of France.",
		"The Eiffel Tower is in Paris.",
		"Berlin is the capital of Germany.",
	},
	TopN: 2,
})
if err != nil {
	log.Fatal(err)
}
for _, r := range result.Results {
	fmt.Println(r.Index, r.Score, r.Document)
}
```

`RerankOpts`:

- **`Model`** (`provider.RerankingModel`, required) — a `nil` model returns
  `ai.ErrModelRequired`.
- **`Query`** (`string`, required) — a `nil`/empty query returns
  `ai.ErrQueryRequired`.
- **`Documents`** (`[]string`, required, non-empty) — an empty slice returns
  `ai.ErrDocumentsRequired`.
- **`TopN`** (`int`, optional) — `0` means "provider default" (typically all
  documents, ranked).
- **`MaxRetries`** (`*int`, default `2`) — same retry wrapper as `Embed`; on
  exhaustion `Rerank` returns a `*ai.RetryError`.
- **`ProviderOptions`** (`map[string]any`) — the usual raw-wire-key escape
  hatch (see [Provider options](provider-options.md)), merged into the
  provider's rerank request the same way as for language-model calls.
- **`OnRerankStart`/`OnRerankEnd`** — fire once around the retried call,
  same start/end and error-translation semantics as `Embed`'s pair:

```go
_, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model:     model,
	Query:     "capital of France",
	Documents: []string{"Paris.", "Berlin."},
	OnRerankStart: func(query string, documents []string) {
		fmt.Println("reranking", len(documents), "documents")
	},
	OnRerankEnd: func(resp *provider.RerankResponse, err error) {
		fmt.Println("rerank finished, err:", err)
	},
})
```

`RerankResult.Results` is `[]ai.RankedDocument{Index, Score, Document}`,
sorted most-relevant first as returned by the provider — `Document` is the
resolved text from `opts.Documents[Index]` (an out-of-range `Index` from the
provider is defensively skipped rather than causing a panic).
`RerankResult.Usage` is a `provider.Usage`.

### Cohere

Cohere is the only rerank-capable provider this wave (Voyage and Mixedbread
are planned alongside their providers in a later wave — see
[Migrating from the Vercel AI SDK](../migrating-from-vercel-ai-sdk.md)).
`Provider.RerankingModel(id)` constructs one, e.g.
`cohere.New().RerankingModel("rerank-v3.5")`. Cohere bills reranking in
"search units," not tokens: `RerankResult.Usage`/`provider.RerankResponse.Usage`
are left zero, and `provider.RerankResponse.Raw` carries the full response
body (including Cohere's own billing metadata) for callers that need it.
See [Cohere § Reranking](../providers/cohere.md#reranking) for the wire
details.

### Via the registry

```go
reg := ai.NewRegistry()
reg.Register("cohere", cohere.New())

model, err := reg.RerankingModel("cohere:rerank-v3.5")
```

`Registry.RerankingModel` resolves the same way as the other four lookups
(`LanguageModel`, `EmbeddingModel`, `ImageModel`, `SpeechModel`,
`TranscriptionModel`) — see
[Middleware and registry § Registry](middleware-and-registry.md#registry).

## Source of truth

- [`ai/embed.go`](../../ai/embed.go)
- [`ai/rerank.go`](../../ai/rerank.go)
- [`ai/similarity.go`](../../ai/similarity.go)
- [`ai/registry.go`](../../ai/registry.go) (`RerankingModel`)
- [`provider/model.go`](../../provider/model.go)
- [`provider/rerank.go`](../../provider/rerank.go)
- [`providers/vertex/embedding.go`](../../providers/vertex/embedding.go)
- [`providers/cohere/rerank.go`](../../providers/cohere/rerank.go)

See also: [Generating text](generating-text.md) for the shared retry
wrapper, [Provider options](provider-options.md) for the general
`ProviderOptions` convention.
