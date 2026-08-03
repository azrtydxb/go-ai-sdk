# Voyage

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/voyage/voyage.go`).

Voyage AI offers no language models through this package — it implements
`provider.EmbeddingModel` (also `provider.EmbeddingModelWithOptions`) and
`provider.RerankingModel`, against Voyage's embeddings and rerank APIs. See
[Embeddings](../core/embeddings.md) and [Embeddings § Reranking](../core/embeddings.md#reranking)
for the `ai.Embed`/`ai.EmbedMany`/`ai.Rerank` call shapes this provider
plugs into.

```go
provider := voyage.New(
	voyage.WithAPIKey("..."),
)
embedder := provider.EmbeddingModel("voyage-3")
reranker := provider.RerankingModel("rerank-2")
```

`WithAPIKey` defaults to `os.Getenv("VOYAGE_API_KEY")`; `WithBaseURL`
defaults to `"https://api.voyageai.com/v1"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as `Authorization: Bearer <key>` on every
request.

## Capabilities

- `Provider.EmbeddingModel(id)` — `provider.EmbeddingModel` (also
  implementing `provider.EmbeddingModelWithOptions`, so `ProviderOptions`
  takes effect on `Embed`/`EmbedMany` calls — see
  [Embeddings § EmbeddingModelWithOptions](../core/embeddings.md#embeddingmodelwithoptions)):
  `POST /embeddings`, `MaxBatchSize() == 128`.
- `Provider.RerankingModel(id)` — `provider.RerankingModel`: `POST /rerank`
  — see [Reranking](#reranking) below.
- No `Model` (language model), `ImageModel`, `SpeechModel`, or
  `TranscriptionModel`.

## Quirks

- **Embeddings are indexed by the response's `index` field**, not appended
  in response order — protects against a provider returning embeddings out
  of order. `EmbedCall`'s wire request is `{"model","input":[...],
  "input_type"?}`; `input_type` is omitted unless set via
  `ProviderOptions["voyage"]`.
- **Embeddings usage is token-based** — `Usage.TotalTokens` comes from the
  response's `usage.total_tokens` field, unlike some rerank-only providers
  that report no usage at all (see Mixedbread's page for the contrast).
- **Rerank request uses `"documents"`**, matching `RerankCall.Documents`
  directly (a bare `[]string]`) — this is the field name Cohere also uses,
  and the one Mixedbread's rerank API does *not* use (Mixedbread's rerank
  request has an `"input"` field instead; see [Mixedbread](mixedbread.md)).
- **`top_k`, not `top_n`, on the wire** — `RerankCall.TopN` maps to Voyage's
  `top_k` field, a `*int` omitted from the wire request entirely when
  `TopN == 0` (matching `RerankCall.TopN`'s own "0 means provider default"
  contract).
- **Rerank usage is also token-based** — `RerankResponse.Usage.TotalTokens`
  comes from the rerank response's `usage.total_tokens` field (Voyage bills
  rerank in tokens, unlike Cohere's "search units" or Mixedbread's
  unreported usage).
- **Results order preserved** — Voyage's rerank response is mapped
  `{index, relevance_score}` → `provider.RankedDocument{Index, Score}` in
  the order returned, with no client-side re-sorting.
- **Error body shape.** `{"detail":"..."}` (`errorMessage` in
  `providers/voyage/wire.go`), with a fallback to the raw body when that
  shape doesn't parse.

## ProviderOptions

`ProviderOptions["voyage"]` is shallow-merged into the built JSON request,
raw wire keys only (see [Provider options](../core/provider-options.md)),
for both embedding and rerank calls:

```go
result, err := ai.Embed(context.Background(), ai.EmbedOpts{
	Model: embedder,
	Value: "hello world",
	ProviderOptions: map[string]any{
		"voyage": map[string]any{
			// no dedicated field for input_type on EmbedOpts
			"input_type": "query",
		},
	},
})
```

Entries under `ProviderOptions["voyage"]` win over whatever the SDK built
from typed fields (`EmbeddingCall`/`RerankCall`).

## Reranking

`Provider.RerankingModel(id)` (e.g. `voyage.New().RerankingModel("rerank-2")`)
constructs a `provider.RerankingModel` that calls Voyage's `POST /rerank`
endpoint. Use it directly, or through `ai.Rerank` — see
[Embeddings § Reranking](../core/embeddings.md#reranking) for the
`ai.Rerank`/`RerankOpts` API and a full example.

```go
model := voyage.New().RerankingModel("rerank-2")

result, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model:     model,
	Query:     "What's the capital of France?",
	Documents: []string{"Paris is the capital of France.", "Berlin is the capital of Germany."},
	TopN:      1,
})
```

- **Model IDs**: Voyage's documented rerank models, e.g. `rerank-2`,
  `rerank-2-lite`. The SDK passes `ModelID()` straight through as the wire
  `model` field — no validation or translation.
- **Request shape** (`providers/voyage/wire.go`: `rerankRequest`): `model`,
  `query`, `documents` (`[]string`), `top_k` (omitted when
  `RerankCall.TopN == 0`).
- **`Usage.TotalTokens`** is populated from `usage.total_tokens` — unlike
  Cohere (which always leaves `Usage` zero, billing in "search units"
  instead).
- **Results**: a `data` array of `{index, relevance_score}`, mapped to
  `provider.RankedDocument{Index, Score}` in response order.

**Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
both the embeddings and rerank integrations are verified only against
`httptest`-replayed fixture responses shaped to match Voyage's published API
docs — neither has been smoke-tested against the live
`https://api.voyageai.com/v1` endpoint.

## Source of truth

- [`providers/voyage/voyage.go`](../../providers/voyage/voyage.go)
- [`providers/voyage/wire.go`](../../providers/voyage/wire.go)
- [`providers/voyage/embedding.go`](../../providers/voyage/embedding.go)
- [`providers/voyage/rerank.go`](../../providers/voyage/rerank.go)
- [`providers/voyage/embedding_test.go`](../../providers/voyage/embedding_test.go)
- [`providers/voyage/rerank_test.go`](../../providers/voyage/rerank_test.go)
  (`TestRerankShapeDiffersFromMixedbread`)
