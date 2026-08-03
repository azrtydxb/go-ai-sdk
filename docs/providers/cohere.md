# Cohere

Cohere's wire format differs enough from OpenAI-compatible chat-completions
shapes — typed SSE payloads dispatched on a `"type"` field, a `"p"` field
for `top_p`, no `tool_choice` field for the "auto" case, and a distinct
embed endpoint/response shape — that this is a standalone implementation
against Cohere's v2 chat and embed APIs.

```go
provider := cohere.New(
	cohere.WithAPIKey("..."),
)
model := provider.Model("command-r-plus")
embedder := provider.EmbeddingModel("embed-english-v3.0")
```

`WithAPIKey` defaults to `os.Getenv("COHERE_API_KEY")`; `WithBaseURL`
defaults to `"https://api.cohere.com/v2"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as `Authorization: Bearer <key>` on every
request (`providers/cohere/language_model.go`, `embedding.go`).

## Capabilities

- `Provider.Model(id)` — `provider.LanguageModel`: chat (`POST /chat`),
  streaming via typed SSE events, tool calling, native JSON response mode
  (`Capabilities().NativeJSON: true`).
- `Provider.EmbeddingModel(id)` — `provider.EmbeddingModel` with
  `MaxBatchSize() == 96` (`providers/cohere/cohere.go`).
- `Provider.RerankingModel(id)` — `provider.RerankingModel`: rerank
  (`POST /rerank`) — see [Reranking](#reranking) below.
- No `ImageModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **`p`, not `top_p`.** `chatRequest.P` is Cohere's wire name for
  `Call.TopP` (`providers/cohere/wire.go`: `P *float64 \`json:"p,omitempty"\`
  // Cohere's name for top_p`). This is also the raw key to use under
  `ProviderOptions["cohere"]`.
- **Text-only chat.** `onlyText` in `providers/cohere/wire.go` rejects any
  non-`TextPart` content in system/user messages with an error — "Cohere
  v2 chat is text-only in this integration" — rather than silently
  dropping images or other content parts.
- **Typed SSE dispatch, not `event:`-based.** Every Cohere v2 streaming
  payload carries its own `"type"` discriminator field (`message-start`,
  `content-delta`, `tool-call-start`, `tool-call-delta`, `tool-call-end`,
  `message-end`, ...); dispatch happens on that field rather than on the
  SSE frame's `event:` line (`providers/cohere/language_model.go`,
  `providers/cohere/wire.go` doc comment on `streamEvent`). `message-end`
  is the only event carrying `finish_reason`; a stream that ends without
  one is reported via `Err()` rather than a fabricated `FinishPart`.
- **`tool_choice` is a bare string, and only sent when needed.** Cohere v2
  has no `tool_choice` field for the "auto" case — tools are just sent and
  Cohere decides. `ToolChoiceRequired` sends `"REQUIRED"` with the full
  tool list; `ToolChoiceTool` also sends `"REQUIRED"` but narrows `Tools`
  down to the single requested tool (Cohere has no per-tool "force this
  one" field); `ToolChoiceNone` omits `Tools` entirely, since Cohere has no
  other way to suppress tool calls (`buildChatRequest` in
  `providers/cohere/wire.go`).
- **Schema is sent, unlike Mistral.** `convertResponseFormat` sends
  `{"type":"json_object","schema":...}` when `ResponseFormat.Schema` is
  set — Cohere v2 does support a schema alongside `json_object`
  (`providers/cohere/wire.go`: "Unlike Mistral, Cohere v2 does support a
  schema alongside the json_object type, so it is included when
  provided.").
- **No error slot on tool-result messages** — same as Mistral: `IsError` is
  not encoded on `RoleTool` messages; there is no dedicated error field in
  Cohere's `tool` message shape (`providers/cohere/wire.go`).
- **Reasoning content is dropped, not replayed** — `ReasoningPart` in an
  assistant message is skipped when building request messages; Cohere has
  no wire representation for reasoning/thinking blocks
  (`providers/cohere/wire.go`).
- **Embeddings default to `input_type: "search_document"`** and always
  request `embedding_types: ["float"]` (`providers/cohere/embedding.go`) —
  there's no `provider.EmbeddingModel` hook to change `input_type` per
  call; use `ProviderOptions` if a different input type is needed on a
  future extension point.

## ProviderOptions

`ProviderOptions["cohere"]` is shallow-merged into the built JSON request,
raw wire keys only (see [Provider options](../core/provider-options.md)).
Verified in `providers/cohere/provideroptions_test.go`:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Summarize this.",
	ProviderOptions: map[string]any{
		"cohere": map[string]any{
			// overrides Call.TopP (wire key is "p", not "top_p")
			"p": 0.9,
			// passthrough key with no typed field
			"frequency_penalty": 0.5,
		},
	},
})
```

## Reranking

`Provider.RerankingModel(id)` (e.g. `cohere.New().RerankingModel("rerank-v3.5")`)
constructs a `provider.RerankingModel` that calls Cohere's `POST /rerank`
endpoint. Use it directly, or through `ai.Rerank` — see
[Embeddings § Reranking](../core/embeddings.md#reranking) for the
`ai.Rerank`/`RerankOpts` API and a full example.

```go
model := cohere.New().RerankingModel("rerank-v3.5")

result, err := ai.Rerank(context.Background(), ai.RerankOpts{
	Model:     model,
	Query:     "What's the capital of France?",
	Documents: []string{"Paris is the capital of France.", "Berlin is the capital of Germany."},
	TopN:      1,
})
```

- **Model IDs**: Cohere's documented rerank models, e.g. `rerank-v3.5`,
  `rerank-english-v3.0`, `rerank-multilingual-v3.0`. The SDK passes
  `ModelID()` straight through as the wire `model` field — no validation or
  translation.
- **Request shape** (`providers/cohere/wire.go`: `rerankRequest`): `model`,
  `query`, `documents` (a bare `[]string`, matching `RerankCall.Documents`),
  and `top_n` — a `*int`, omitted entirely from the wire request when
  `RerankCall.TopN == 0` (`json:"top_n,omitempty"` on a pointer field, so an
  intentional `0` is indistinguishable from "unset" — which matches
  `RerankCall.TopN`'s own contract that `0` means "provider default").
  `ProviderOptions["cohere"]` is shallow-merged in last, same convention as
  chat requests (raw wire keys, e.g. Cohere's `rank_fields` or
  `max_tokens_per_doc` — not otherwise exposed by this SDK).
- **`Usage` is always zero.** Cohere bills reranking in "search units," not
  tokens — there is no token-usage field in the rerank response to map onto
  `provider.Usage`. `provider.RerankResponse.Raw` preserves the full,
  unparsed response body (including Cohere's `meta.billed_units.search_units`
  field) for callers that need billing detail.
- **Results**: Cohere's response is a `results` array of
  `{index, relevance_score}`, already sorted most-relevant first; the SDK
  maps `relevance_score` onto `provider.RankedDocument.Score` and passes
  `index` through unchanged.

**Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
the rerank integration is verified only against `httptest`-replayed fixture
responses shaped to match Cohere's published rerank API docs — it has not
been smoke-tested against the live `https://api.cohere.com/v2/rerank`
endpoint.

## Source of truth

- [`providers/cohere/cohere.go`](../../providers/cohere/cohere.go)
- [`providers/cohere/language_model.go`](../../providers/cohere/language_model.go)
- [`providers/cohere/wire.go`](../../providers/cohere/wire.go)
- [`providers/cohere/embedding.go`](../../providers/cohere/embedding.go)
- [`providers/cohere/rerank.go`](../../providers/cohere/rerank.go)
- [`providers/cohere/provideroptions_test.go`](../../providers/cohere/provideroptions_test.go)
- [`providers/cohere/rerank_test.go`](../../providers/cohere/rerank_test.go)
