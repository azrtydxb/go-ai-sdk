# Vercel AI Gateway

Vercel AI Gateway is an OpenAI-compatible routing endpoint that fronts many
upstream model providers (OpenAI, Anthropic, Google, and more) behind
`"provider/model"` routing slugs such as `"openai/gpt-4o"` or
`"anthropic/claude-3-5-sonnet"`. Since the Gateway fronts heterogeneous
upstreams, `providers/gateway` is a preset over the shared
`internal/openaicompat` base with `NativeJSON` left conservative (`false`).

```go
import "github.com/azrtydxb/go-ai-sdk/providers/gateway"

// APIKey defaults to os.Getenv("AI_GATEWAY_API_KEY"); BaseURL defaults to
// "https://ai-gateway.vercel.sh/v1". Auth is sent as
// "Authorization: Bearer <key>".
provider := gateway.New(
	gateway.WithAPIKey("..."), // optional, overrides AI_GATEWAY_API_KEY
	gateway.WithBaseURL("..."), // optional, overrides the default base URL
)
model := provider.Model("openai/gpt-4o")
embedder := provider.EmbeddingModel("openai/text-embedding-3-small")
```

`gateway.New` also accepts `gateway.WithHTTPClient(*http.Client)` to override
the client used for requests.

## Supported capabilities

- **Text generation and streaming** — `gateway.New().Model(slug)`, used with
  `ai.GenerateText` / `ai.StreamText`. The `slug` (e.g. `"openai/gpt-4o"`)
  is sent verbatim as the wire `model` field — openaicompat treats it as an
  opaque string, so slashes in the id need no special handling.
- **Tool calling** — standard `provider.Call.Tools` / `ToolChoice`.
- **Embeddings** — `gateway.New().EmbeddingModel(slug)`,
  `MaxBatchSize() == 1` (`openaicompat.Config.EmbedBatch: 1`).

Not wired for this preset: no image, speech, or transcription support.

## Quirks and notes

- **`NativeJSON: false`.** The Gateway routes to heterogeneous upstreams
  (OpenAI, Anthropic, Google, ...) whose native-JSON/schema support differs
  per slug, so this preset is conservative and does not advertise
  `Capabilities().NativeJSON` as `true`; `ai.GenerateObject` falls back to
  tool-mode structured output (see
  [Structured output](../core/structured-output.md)).
- **`EmbedBatch: 1`.** Batch-size limits vary per upstream embedding model
  behind a slug, so this preset uses the safe floor rather than guessing a
  number that could silently overflow some upstream's limit.
- **Registry routing slugs with slashes round-trip correctly.**
  `ai.Registry`'s `splitID` (`ai/registry.go`) cuts a `"provider:model"`
  string on the *first* colon only, so `"gateway:openai/gpt-4o"` resolves
  to provider name `"gateway"` and model `"openai/gpt-4o"` — the slug's
  internal slash is never mistaken for the registry separator.
- **OIDC is out of scope.** Vercel also supports an OIDC token flow
  (`VERCEL_OIDC_TOKEN`, used implicitly inside Vercel deployments) as an
  alternative to a static API key; this package does not read
  `VERCEL_OIDC_TOKEN` — only the `AI_GATEWAY_API_KEY` → `Authorization:
  Bearer` flow is supported. A future extension would need a new
  `Config`/header path in `openaicompat` or a Gateway-specific
  `http.RoundTripper`, since `openaicompat` currently assumes a single
  static API key.
- No other provider-specific `Config` quirks — see
  [`providers/gateway/gateway.go`](../../providers/gateway/gateway.go) for
  the full preset.

⚠ **Live-testing note:** like every provider in this SDK (see
[Provider overview § Live-testing status](README.md#live-testing-status)),
this preset is verified only against `httptest`-replayed fixture responses
shaped to match the shared `openaicompat` conformance suite — it has not
been smoke-tested against the live `https://ai-gateway.vercel.sh/v1`
endpoint.

## ProviderOptions

Raw wire keys are merged verbatim into the request body (see
[Provider options](../core/provider-options.md)):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "List three prime numbers.",
	ProviderOptions: map[string]any{
		"gateway": map[string]any{
			"temperature": 0.9,
		},
	},
})
```

Keys not covered by a typed `provider.Call` field pass straight through;
entries here take priority over anything the SDK built from typed fields.
The same convention applies to embedding calls via
`ProviderOptions["gateway"]`.

## Via the registry

```go
reg := ai.NewRegistry()
reg.Register("gateway", gateway.New())

model, err := reg.LanguageModel("gateway:anthropic/claude-3-5-sonnet")
```

See [Middleware and registry § Registry](../core/middleware-and-registry.md#registry)
for `Registry.LanguageModel`/`EmbeddingModel` in full.

## Source of truth

- [`providers/gateway/gateway.go`](../../providers/gateway/gateway.go)
- [`providers/gateway/gateway_test.go`](../../providers/gateway/gateway_test.go)
  (`TestRegistryRoundTrip`, verifying the slash-containing slug survives
  `Registry` lookup unchanged)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/embedding.go`](../../internal/openaicompat/embedding.go)
- [`ai/registry.go`](../../ai/registry.go) (`splitID`)
