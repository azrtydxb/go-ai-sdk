# Perplexity

Perplexity's API is OpenAI chat-completions compatible, so this provider is
a thin preset over the shared `internal/openaicompat` base rather than a
standalone implementation.

```go
model := perplexity.New(
	perplexity.WithAPIKey("pplx-..."),
).Model("sonar-pro")
```

`WithAPIKey` defaults to `os.Getenv("PERPLEXITY_API_KEY")`; `WithBaseURL`
defaults to `"https://api.perplexity.ai"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as `Authorization: Bearer <key>`, the
`openaicompat` default (no custom `APIKeyHeader` is configured).

## Capabilities

- `Provider.Model(id)` — `provider.LanguageModel`: chat, streaming, native
  JSON response mode (`Capabilities().NativeJSON` is `true`).
- No `EmbeddingModel`, `ImageModel`, `SpeechModel`, or `TranscriptionModel`
  — the preset only calls `openaicompat.NewLanguageModel`.

## Quirks

- **No live tool calling.** From the package doc comment in
  `providers/perplexity/perplexity.go`: "Perplexity's API does not support
  tool calling; Tools in a Call are serialized but the live API may reject
  or ignore them." The SDK still serializes `Call.Tools` onto the wire
  request the same way any other `openaicompat` provider does — there is
  no code-level guard that strips or rejects them — but Perplexity's own
  API is the one that doesn't honor the tool-calling contract, so a
  `Tools`-driven step/tool loop should not be relied on against this
  provider.
- **`max_tokens`, not `max_completion_tokens`.** Perplexity documents the
  older `max_tokens` field name; OpenAI's current `max_completion_tokens`
  is silently ignored by Perplexity's API. The preset sets
  `Config.MaxTokensParam: "max_tokens"` to route `Call.MaxTokens` onto the
  field Perplexity actually reads (`providers/perplexity/perplexity.go`).
- **`NativeJSON: true`** — Perplexity's `response_format` supports
  `json_schema`, so `provider.ResponseFormat.Schema` is sent through rather
  than dropped (contrast with Mistral, which only ever sends
  `json_object`).

## ProviderOptions

Perplexity uses the same namespaced-map/wire-key convention as every other
`openaicompat` preset (see [Provider options](../core/provider-options.md)):
entries under `ProviderOptions["perplexity"]` are shallow-merged into the
already-built JSON request, so any raw Perplexity wire key works, including
ones with no dedicated `provider.Call` field:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "What happened in Go 1.23?",
	ProviderOptions: map[string]any{
		"perplexity": map[string]any{
			// overrides Call.Temperature
			"temperature": 0.2,
			// passthrough key with no typed field
			"search_recency_filter": "week",
		},
	},
})
```

`internal/openaicompat/provideroptions_test.go` verifies the merge
mechanism generically (temperature override plus a novel passthrough key,
e.g. `logprobs`); the same mechanism applies to Perplexity's own wire keys
since the preset shares `openaicompat`'s `applyProviderOptions`.

## Source of truth

- [`providers/perplexity/perplexity.go`](../../providers/perplexity/perplexity.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
