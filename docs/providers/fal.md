# fal

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/fal/fal.go`).

fal.ai offers no language models through this package — it only implements
`provider.ImageModel`, against fal.ai's synchronous `fal.run` image-generation
endpoint. See [Media: images, speech, transcription](../core/media.md) for
the `ai.GenerateImage` call shape this provider plugs into.

```go
provider := fal.New(
	fal.WithAPIKey("..."),
)
image := provider.ImageModel("fal-ai/flux/schnell")
```

`WithAPIKey` defaults to `os.Getenv("FAL_API_KEY")`, falling back to
`os.Getenv("FAL_KEY")` when that's empty; `WithBaseURL` defaults to
`"https://fal.run"`; `WithHTTPClient` overrides the `*http.Client` used for
both the generation request and any resulting image URL downloads. Auth is
sent as the `Authorization: Key <key>` header (not `Bearer`) on every
request.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`: `POST /{modelID}`
  against the configured base URL (e.g. `fal.run/fal-ai/flux/schnell`),
  JSON body, JSON response containing image URLs (or inline `data:` URLs).
- No `Model`, `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **Size only, no aspect ratio.** A non-empty `ImageCall.AspectRatio`
  returns `"fal: aspect ratio is not supported; use Size"`
  (`providers/fal/image.go`); `Size` is sent through as fal's
  `image_size` field.
- **Two ways to receive an image.** fal's response returns each image as
  either an `https://` URL (fetched via `internal/fetchimage.Fetch`) or an
  inline `data:<mediatype>;base64,<data>` URL (decoded locally,
  `providers/fal/image.go`'s `decodeDataURL`). Either way, a non-empty
  `content_type` field on the wire response overrides the resolved
  `MediaType`.
- **Error body shapes.** fal's error responses use `{"detail":"..."}` or,
  for FastAPI-style validation errors, `{"detail":[{"msg":"..."}, ...]}`;
  `errorMessage` in `providers/fal/fal.go` tries both shapes before
  falling back to the raw body.
- **`Seed` passthrough, `N` as `num_images`.** `ImageCall.N` maps to
  `num_images`, `ImageCall.Seed` maps to `seed` when non-nil; both are
  omitted from the wire request when unset.

## ProviderOptions

Verified in `providers/fal/image_test.go`
(`TestGenerateImages_ProviderOptionsMergeTopLevel`):

```go
_, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  image,
	Prompt: "a red bicycle on a white background",
	ProviderOptions: map[string]any{
		"fal": map[string]any{
			// overrides the SDK-built prompt
			"prompt":              "overridden prompt",
			// passthrough keys with no typed field
			"guidance_scale":      7.5,
			"num_inference_steps": 4,
		},
	},
})
```

`ProviderOptions["fal"]` entries are merged top-level into the marshaled
JSON request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/fal/fal.go`).

## Source of truth

- [`providers/fal/fal.go`](../../providers/fal/fal.go)
- [`providers/fal/image.go`](../../providers/fal/image.go)
- [`providers/fal/image_test.go`](../../providers/fal/image_test.go)
