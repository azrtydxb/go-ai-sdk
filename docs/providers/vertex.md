# Vertex AI

`providers/vertex` talks to Google Cloud's Vertex AI API — Gemini models
hosted on Google Cloud, signed with an OAuth2 bearer token rather than an
API key. Language-model requests reuse `internal/geminicompat` (the same
wire format `providers/google` speaks); embeddings use Vertex's own
`:predict` format and are implemented directly in this package.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/vertex"

p := vertex.New(
	vertex.WithProject("my-gcp-project"),  // defaults to os.Getenv("GOOGLE_VERTEX_PROJECT")
	vertex.WithLocation("us-central1"),    // defaults to os.Getenv("GOOGLE_VERTEX_LOCATION"), else "us-central1"
	// one of:
	vertex.WithAccessToken("ya29...."),           // fixed bearer token (tests, short-lived tokens)
	vertex.WithTokenSource(myTokenSource),        // any gauth.TokenSource
	// or leave both unset and set GOOGLE_APPLICATION_CREDENTIALS to a
	// service-account JSON key file path — New() loads it automatically.
)

model := p.Model("gemini-2.0-flash")
```

## Authentication paths (verified from `providers/vertex/vertex.go`)

There are three ways credentials get resolved, in this precedence order:

1. **`WithTokenSource(ts gauth.TokenSource)`** — takes precedence over
   everything else, including automatic discovery
   (`providers/vertex/vertex.go:49-54`).
2. **`WithAccessToken(token string)`** — sugar for `WithTokenSource` wrapping
   a `gauth.StaticTokenSource` that always returns the fixed string
   (`providers/vertex/vertex.go:56-61`).
3. **Automatic discovery from `GOOGLE_APPLICATION_CREDENTIALS`** — if
   neither of the above is set, `New()` checks that env var and, if set,
   loads a service-account token source from the JSON key file at that path
   via `gauth.NewServiceAccountTokenSourceFromFile`
   (`providers/vertex/vertex.go:70-99`). If the env var is unset, requests
   fail at call time with `"vertex: no credentials configured"`; if it's set
   but the file can't be loaded, requests fail with that load error instead
   — `New()` itself never returns an error, it defers to the first request.

**This is not Google's full Application Default Credentials chain** — there
is no metadata-server lookup, no `gcloud auth application-default login`
user-credentials file support, no workload-identity federation. `internal/gauth`
is a from-scratch, standard-library-only implementation of just the RS256 JWT
bearer grant for a service-account key
(`internal/gauth/gauth.go:1-4`, package doc comment), minting Cloud Platform
OAuth2 access tokens and caching them until shortly before expiry. If your
deployment relies on a broader ADC mechanism (GCE/GKE metadata server, user
`gcloud` credentials), you must implement `gauth.TokenSource` yourself and
pass it via `WithTokenSource`.

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)`, via
  `geminicompat.NewLanguageModel`, same wire format as `providers/google`.
- **Tool calling** — same `Model(id)`.
- **Structured output (native JSON)** — inherited from `geminicompat`;
  `Capabilities().NativeJSON` is `true`.
- **Embeddings** — `p.EmbeddingModel(id)`, e.g.
  `p.EmbeddingModel("text-embedding-004")`; implemented directly in this
  package (not via `geminicompat`) against Vertex's generic Prediction API
  (`:predict`), batching up to 250 inputs per call (`embeddingMaxBatchSize`,
  `providers/vertex/vertex.go:24`).
- **Image generation (Imagen)** — `p.ImageModel(id)`, via
  `geminicompat.NewImageModel`; accepts `AspectRatio`, not `Size` — see
  [Media](../core/media.md#size-vs-aspectratio).

## Quirks and notes

- **The `"global"` location has no regional URL prefix.** Vertex normally
  requires a region like `us-central1`, producing
  `https://{location}-aiplatform.googleapis.com/v1`; but `location ==
  "global"` is special-cased to `https://aiplatform.googleapis.com/v1`
  directly — the source comment is explicit that
  `global-aiplatform.googleapis.com` **does not exist**:
  > "The `\"global\"` location has no regional prefix: it is served from
  > `aiplatform.googleapis.com` directly, not
  > `global-aiplatform.googleapis.com` (which does not exist)."
  (`providers/vertex/vertex.go:108-119`, `resolvedBaseURL`.)
- **Project ID is required at request time, not construction time.**
  `New()` never validates `project`; an empty project ID only surfaces as a
  malformed endpoint URL (`/projects//locations/.../models/...`) once a
  call is made. `WithProject`/`GOOGLE_VERTEX_PROJECT` is the only way to set
  it — there's no separate discovery path from the credentials file.
- **Embeddings bypass `geminicompat` entirely.** Unlike the language and
  image models, `EmbeddingModel` is implemented directly in
  `providers/vertex/embedding.go` against Vertex's generic `:predict`
  format (`wireInstance{Content}` → `wireEmbeddings{Values, Statistics}`),
  because that wire shape differs from Gemini's own
  `batchEmbedContents`/`embedContent` format that `providers/google` uses.
  (`providers/vertex/embedding.go:1-18`, package comment.)
- **Embedding response count is verified, not zipped positionally.**
  `:predict` returns predictions with no per-prediction identifier to
  correlate them back to input values; if the response's prediction count
  doesn't match the request's input count, `EmbedCall` returns an error
  rather than silently mismatching results to values.
  (`providers/vertex/embedding.go:152-158`.)
- **Language and image models share `geminicompat`, so the same quirks from
  the [Google provider page](google.md) apply here too** — schema
  sanitization (`additionalProperties` stripped), name-based tool-result
  correlation, synthesized tool-call IDs, non-replayable reasoning parts.

## ProviderOptions

Vertex's language model is `geminicompat.NewLanguageModel`, whose
`ProviderName()` is configured as `"vertex"`
(`providers/vertex/vertex.go:141-149`, `Config.Name`), so chat calls merge
`ProviderOptions["vertex"]` into the same `generateContent` wire shape
documented on the [Google page](google.md#provideroptions). Embedding
calls separately merge `ProviderOptions["vertex"]` into the `:predict` body
(`instances`, `parameters`) — verified against
`providers/vertex/provideroptions_test.go`: an option key can override an
SDK-built field (`instances`), and a novel key not otherwise exposed by the
SDK (`parameters.autoTruncate`) passes straight through:

```go
optioned := model.(provider.EmbeddingModelWithOptions)
_, err := optioned.EmbedCall(context.Background(), provider.EmbeddingCall{
	Values: []string{"hello"},
	ProviderOptions: map[string]any{
		"vertex": map[string]any{
			"parameters": map[string]any{"autoTruncate": false}, // passthrough
		},
	},
})
```

## Source of truth

- [`providers/vertex/vertex.go`](../../providers/vertex/vertex.go)
  (package doc comment, `Option`s, `resolvedBaseURL`, `authorize`)
- [`providers/vertex/embedding.go`](../../providers/vertex/embedding.go)
  (`:predict` wire types, `EmbedCall`, `applyProviderOptions`)
- [`providers/vertex/provideroptions_test.go`](../../providers/vertex/provideroptions_test.go)
- [`internal/gauth/gauth.go`](../../internal/gauth/gauth.go) (package doc
  comment, `TokenSource`, `StaticTokenSource`, service-account JWT bearer
  flow)
- [`internal/geminicompat/wire.go`](../../internal/geminicompat/wire.go)
  (shared with `providers/google`; see the [Google page](google.md) for
  those quirks)

See also: [Google (Gemini Developer API)](google.md) for the shared
`geminicompat` wire behavior; [Media](../core/media.md) for the
Size/AspectRatio split on image calls.
