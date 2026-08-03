# Mixedbread

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/mixedbread/mixedbread.go`).

Mixedbread AI offers no language models or embeddings through this package
— it implements only `provider.RerankingModel`, against Mixedbread's rerank
API. See [Embeddings § Reranking](../core/embeddings.md#reranking) for the
`ai.Rerank` call shape this provider plugs into.

```go
provider := mixedbread.New(
	mixedbread.WithAPIKey("..."),
)
reranker := provider.RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")
```

`WithAPIKey` defaults to `os.Getenv("MXBAI_API_KEY")`; `WithBaseURL`
defaults to `"https://api.mixedbread.com/v1"`; `WithHTTPClient` overrides
the `*http.Client`. Auth is sent as `Authorization: Bearer <key>` on every
request.

## Capabilities

- `Provider.RerankingModel(id)` — `provider.RerankingModel`: `POST /rerank`
  — see [Reranking](#reranking) below.
- No `EmbeddingModel`, `Model` (language model), `ImageModel`,
  `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **`input`, not `documents`, on the wire.** Mixedbread's rerank request
  field name differs from both Cohere's and Voyage's `documents` —
  `RerankCall.Documents` is sent as the wire `input` array
  (`providers/mixedbread/rerank.go`: `rerankRequest.Input`).
- **`return_input` is always sent, and always `false`.** Unlike Voyage's/
  Cohere's optional pointer fields, `return_input` is a plain `bool` with no
  `omitempty` — this SDK reconstructs documents from `RerankCall.Documents`
  + `RankedDocument.Index` rather than relying on Mixedbread echoing the
  input back, so it always requests `return_input: false`.
- **`score`, not `relevance_score`, on the wire response.** Mixedbread's
  per-result field is named `score`
  (`providers/mixedbread/rerank.go`: `rerankResultWire.Score`), unlike
  Cohere/Voyage's `relevance_score` — mapped onto
  `provider.RankedDocument.Score` the same way regardless of the provider's
  field name.
- **`top_k`, not `top_n`, on the wire** — `RerankCall.TopN` maps to
  Mixedbread's `top_k` field, a `*int` omitted entirely when `TopN == 0`.
- **`Usage` is always zero.** Mixedbread's rerank response in the brief's
  documented shape carries no token-usage field — `provider.RerankResponse.Usage`
  is left zero-valued (same precedent as Cohere, which bills reranking in
  "search units" instead of tokens; unlike Voyage, which does report
  `usage.total_tokens` for rerank). `provider.RerankResponse.Raw` preserves
  the full, unparsed response body for callers that need it.
- **Results order preserved** — mapped from the response's `data` array in
  the order returned, with no client-side re-sorting.
- **Error body shape.** `{"detail":"..."}` (`errorMessage` in
  `providers/mixedbread/mixedbread.go`), with a fallback to the raw body
  when that shape doesn't parse.

## ProviderOptions

`ProviderOptions["mixedbread"]` is shallow-merged into the built JSON
request, raw wire keys only (see [Provider options](../core/provider-options.md)):

```go
result, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model: reranker,
	Query: "What's the capital of France?",
	Documents: []string{
		"Paris is the capital of France.",
		"Berlin is the capital of Germany.",
	},
	ProviderOptions: map[string]any{
		"mixedbread": map[string]any{
			// overrides the SDK-built model field
			"model": "mixedbread-ai/mxbai-rerank-xsmall-v1",
		},
	},
})
```

Entries under `ProviderOptions["mixedbread"]` win over whatever the SDK
built from `RerankCall`'s typed fields.

## Reranking

`Provider.RerankingModel(id)` (e.g.
`mixedbread.New().RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")`)
constructs a `provider.RerankingModel` that calls Mixedbread's `POST /rerank`
endpoint. Use it directly, or through `ai.Rerank` — see
[Embeddings § Reranking](../core/embeddings.md#reranking) for the
`ai.Rerank`/`RerankOpts` API and a full example.

```go
model := mixedbread.New().RerankingModel("mixedbread-ai/mxbai-rerank-large-v1")

result, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model:     model,
	Query:     "What's the capital of France?",
	Documents: []string{"Paris is the capital of France.", "Berlin is the capital of Germany."},
	TopN:      1,
})
```

- **Model IDs**: Mixedbread's documented rerank models, e.g.
  `mixedbread-ai/mxbai-rerank-large-v1`. The SDK passes `ModelID()` straight
  through as the wire `model` field — no validation or translation.
- **Request shape** (`providers/mixedbread/rerank.go`: `rerankRequest`):
  `model`, `query`, `input` (`[]string`, **not** `documents`), `top_k`
  (omitted when `RerankCall.TopN == 0`), `return_input` (always `false`).
- **`Usage`** is always zero — see Quirks above.
- **Results**: a `data` array of `{index, score}`, mapped to
  `provider.RankedDocument{Index, Score}` in response order.

**Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
the rerank integration is verified only against `httptest`-replayed fixture
responses shaped to match Mixedbread's published rerank API docs — it has
not been smoke-tested against the live `https://api.mixedbread.com/v1`
endpoint.

## Source of truth

- [`providers/mixedbread/mixedbread.go`](../../providers/mixedbread/mixedbread.go)
- [`providers/mixedbread/rerank.go`](../../providers/mixedbread/rerank.go)
- [`providers/mixedbread/rerank_test.go`](../../providers/mixedbread/rerank_test.go)
  (`TestRerankRequestShape`, `TestRerankResponseShapeUsesScoreNotRelevanceScore`)
