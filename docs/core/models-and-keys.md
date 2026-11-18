# Model listing and key checks

Two provider-level capabilities, shaped like `provider.FileStore`: ask a
provider which models it offers, and check that an API key works. Both are
optional interfaces. You get them from a constructed provider (they are not
wired into `ai.Registry`) and call them through `ai.ListModels` /
`ai.CheckKey`, which add the standard retry logic (`MaxRetries`, default 2).

## ListModels

```go
p := openaicompatible.New("https://openrouter.ai/api/v1",
	openaicompatible.WithAPIKey(key),
	openaicompatible.WithName("openrouter"))

models, err := ai.ListModels(ctx, ai.ListModelsOpts{
	Lister:         p.ModelLister(),
	OutputModality: "speech", // OpenRouter: sent as ?output_modalities=speech
})
for _, m := range models {
	fmt.Println(m.ID, m.Name, m.Voices) // Voices: OpenRouter's supported_voices
}
```

`provider.ModelInfo` has the provider-neutral fields `ID`, `Name`,
`Description`, `OwnedBy`, `Created`, `ContextLength`, `InputModalities`,
`OutputModalities` and `Voices`. `Raw` holds the model's JSON object, so you
can still read anything else (pricing, supported parameters, ...). Fields a
provider does not report stay at their zero value.

Filtering:

- `OutputModality` is sent server-side where the provider supports it
  (OpenRouter's `output_modalities`) and is also applied to the decoded list.
  OpenRouter lists only text-output models unless you ask, so set it to
  `"speech"`, `"image"`, ..., or to `"all"` for everything.
- `InputModality` is applied client-side only.
- A model that reports no modalities at all (plain OpenAI, vLLM, Ollama) is
  never filtered out, because nothing says it lacks the modality.

`ai.ErrListerRequired` is returned when `Lister` is nil.

## CheckKey

```go
info, err := ai.CheckKey(ctx, ai.CheckKeyOpts{Checker: p.KeyChecker()})
var api *ai.APICallError
switch {
case errors.As(err, &api) && (api.StatusCode == 401 || api.StatusCode == 403):
	// the key was rejected
case err != nil:
	// transport failure, 5xx after retries, ...
case info.Detailed:
	fmt.Println(info.Label) // Limit, Usage, LimitRemaining are *float64; nil Limit = unlimited
}
```

A nil error means the key was accepted. A rejected key comes back as an
`*ai.APICallError` with status 401 or 403, which is not retried. When the
provider has a key-info endpoint, `KeyInfo.Detailed` is true and `Label`,
`Limit`, `Usage`, `LimitRemaining`, `FreeTier` and `Raw` are filled in.
Without one, the key is validated by an authenticated request and only
`Detailed == false` is returned.

`ai.ErrCheckerRequired` is returned when `Checker` is nil.

## Provider matrix

| Provider           | `ModelLister()`                                                     | `KeyChecker()`                                                                                           |
| ------------------ | ------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `openaicompatible` | `GET /models`, `output_modalities` query, OpenRouter extras         | `GET /key` (OpenRouter; path set by `WithKeyInfoPath`), then `GET /models` on a 404/405 or unknown shape |
| `openai`           | `GET /models` (id, created, owned_by only)                          | `GET /models`, so the result is never `Detailed`                                                         |
| `gateway`          | `GET /models` (name, description, context window; pricing in `Raw`) | not available                                                                                            |

The `/models` fallback proves a key only where `/models` requires
authentication. On OpenAI and most hosted APIs it does. OpenRouter's
`/models` is public, which is why `openaicompatible` reads `/key` first.

## Source of truth

- [`provider/models.go`](../../provider/models.go)
- [`ai/list_models.go`](../../ai/list_models.go)
- [`internal/openaicompat/models.go`](../../internal/openaicompat/models.go)
