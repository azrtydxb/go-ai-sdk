# Prodia

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/prodia/prodia.go`).

Prodia offers no language models through this package — it only implements
`provider.ImageModel`, against Prodia's v2 synchronous inference API. See
[Media: images, video, speech, transcription, translation](../core/media.md)
for the `ai.GenerateImage` call shape this provider plugs into.

```go
provider := prodia.New(
	prodia.WithAPIKey("..."),
)
image := provider.ImageModel("inference.flux.schnell.txt2img")
```

`WithAPIKey` defaults to `os.Getenv("PRODIA_API_KEY")`; `WithBaseURL`
defaults to `"https://inference.prodia.com/v2"`; `WithHTTPClient` overrides
the `*http.Client`. Auth is sent as `Authorization: Bearer <key>` on every
request, with `Accept: image/jpeg`.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`: `POST /job` against
  Prodia's v2 synchronous job endpoint. The response body **is** the
  generated image bytes directly — not a JSON envelope.
- No `Model`, `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **Job type, not a model ID, in the usual sense.** `ImageModel(id)`'s `id`
  (e.g. `"inference.flux.schnell.txt2img"`) is sent as the request's
  top-level `type` field; the model-tunable knobs live in a nested
  `config` object.
- **`Size` ("WxH") splits into `width`/`height`.** `ImageCall.Size` is
  parsed via `strings.Cut(size, "x")` into `config.width`/`config.height`
  (both `omitempty`, only set on a successful parse) — there's no
  aspect-ratio equivalent for this provider.
- **Provider options merge into `config`, not top-level.** Unlike most
  image providers (which merge `ProviderOptions` into the top-level request
  body), Prodia's tunable parameters (`prompt`, `width`, `height`, `seed`,
  and anything else the target job type accepts) live under `config`, so
  `ProviderOptions["prodia"]` is merged there instead
  (`providers/prodia/image.go`).
- **Response body IS the image — no JSON envelope, no `Raw`.**
  `provider.ImageResponse.Images[0]` is built directly from the raw
  response bytes (`MediaType: "image/jpeg"`, matching the `Accept` header
  sent); `ImageResponse.Raw` is left `nil` since there's no JSON envelope
  to preserve.
- **`Seed` passthrough**, a `*int64` mapped to `config.seed` when non-nil,
  `omitempty` otherwise.
- **Error body shapes.** `{"error":"..."}` or `{"message":"..."}`
  (`errorMessage` in `providers/prodia/prodia.go`), with a fallback to the
  raw body when neither shape parses.

## ProviderOptions

Verified in `providers/prodia/image_test.go`:

```go
_, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  image,
	Prompt: "a red bicycle on a white background",
	Size:   "512x512",
	ProviderOptions: map[string]any{
		"prodia": map[string]any{
			// overrides the SDK-built prompt (merges into "config", not top-level)
			"prompt": "overridden prompt",
			// passthrough key with no typed field
			"steps": 25,
		},
	},
})
```

`ProviderOptions["prodia"]` entries are merged into the marshaled JSON
`config` object, winning over whatever the SDK built
(`applyProviderOptions` in `providers/prodia/prodia.go`).

## Source of truth

- [`providers/prodia/prodia.go`](../../providers/prodia/prodia.go)
- [`providers/prodia/image.go`](../../providers/prodia/image.go)
- [`providers/prodia/image_test.go`](../../providers/prodia/image_test.go)
