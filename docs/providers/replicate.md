# Replicate

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/replicate/replicate.go`).

Replicate offers no language models through this package — it only
implements `provider.ImageModel`, against Replicate's synchronous
(`Prefer: wait`) predictions API. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.GenerateImage` call shape this provider plugs into.

```go
provider := replicate.New(
	replicate.WithAPIKey("..."),
)
image := provider.ImageModel("black-forest-labs/flux-schnell")
```

`WithAPIKey` defaults to `os.Getenv("REPLICATE_API_TOKEN")`; `WithBaseURL`
defaults to `"https://api.replicate.com"`; `WithHTTPClient` overrides the
`*http.Client` used for both the prediction request and any resulting image
URL downloads. Auth is sent as the standard `Authorization: Bearer <key>`
header.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`:
  `POST /v1/models/{modelID}/predictions` with header `Prefer: wait` (so
  the call blocks until the prediction resolves rather than requiring a
  separate poll), JSON body, JSON response.
- No `Model`, `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **Aspect ratio only, no size.** A non-empty `ImageCall.Size` returns
  `"replicate: size is not supported; use AspectRatio"`
  (`providers/replicate/image.go`); `AspectRatio` is sent through as
  `input.aspect_ratio`.
- **`ProviderOptions` nests under `input`, not top-level.** This is the
  documented divergence from most other providers' convention: Replicate's
  predictions API nests all model parameters under an `"input"` object, so
  `ProviderOptions["replicate"]` entries are merged into `input` (creating
  it if absent) rather than merged top-level into the request body — see
  `applyProviderOptions` in `providers/replicate/replicate.go`.
- **`output` may be a string or an array.** Replicate's prediction
  response's `output` field is either a single URL string or an array of
  URL strings depending on the model; `outputURLs` in
  `providers/replicate/image.go` normalizes both shapes into a slice
  before fetching each URL.
- **Non-`"succeeded"` status is an error**, even after the synchronous
  wait — `predictionResponse.Status != "succeeded"` returns an error that
  includes `predictionResponse.Error` when present (which itself may be a
  JSON string or an arbitrary JSON value, handled by `errorText`).
- **`Prefer: wait` has a ~60s ceiling.** Replicate holds the HTTP connection
  open for at most about 60 seconds while waiting synchronously; a model
  that's still running when the window elapses legitimately returns a
  `"processing"` status rather than `"succeeded"`, which this SDK surfaces
  as the non-`"succeeded"` error above rather than as a failure — slow
  models can hit this even on a correct call, so treat it as "still
  running," not "broken."
- **Error body shapes.** Replicate's error responses use either
  `{"detail":"..."}` (e.g. auth errors) or an RFC-7807-style problem object
  with `"title"`/`"detail"`; `errorMessage` in
  `providers/replicate/replicate.go` prefers `detail`, then `title`.

## ProviderOptions

Verified in `providers/replicate/image_test.go`
(`TestGenerateImages_ProviderOptionsMergeIntoInput`):

```go
_, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  image,
	Prompt: "a red bicycle on a white background",
	ProviderOptions: map[string]any{
		"replicate": map[string]any{
			// overrides the SDK-built input.prompt
			"prompt":              "overridden prompt",
			// passthrough keys with no typed field, also under input
			"guidance":            3.5,
			"num_inference_steps": 4,
		},
	},
})
```

## Source of truth

- [`providers/replicate/replicate.go`](../../providers/replicate/replicate.go)
- [`providers/replicate/image.go`](../../providers/replicate/image.go)
- [`providers/replicate/image_test.go`](../../providers/replicate/image_test.go)
