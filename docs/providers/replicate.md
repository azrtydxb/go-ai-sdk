# Replicate

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/replicate/replicate.go`).

Replicate offers no language models through this package — it implements
`provider.ImageModel` and `provider.VideoModel`, both against Replicate's
synchronous (`Prefer: wait`) predictions API. See
[Media: images, video, speech, transcription, translation](../core/media.md)
for the `ai.GenerateImage`/`ai.GenerateVideo` call shapes this provider
plugs into.

```go
provider := replicate.New(
	replicate.WithAPIKey("..."),
)
image := provider.ImageModel("black-forest-labs/flux-schnell")
video := provider.VideoModel("minimax/video-01")
```

`WithAPIKey` defaults to `os.Getenv("REPLICATE_API_TOKEN")`; `WithBaseURL`
defaults to `"https://api.replicate.com"`; `WithHTTPClient` overrides the
`*http.Client` used for both the prediction request and any resulting image
URL downloads; `WithPollInterval` overrides the interval between
`VideoModel` status polls (default 500ms — a test hook, mirroring
`providers/luma`'s `WithPollInterval`). Auth is sent as the standard
`Authorization: Bearer <key>` header.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`:
  `POST /v1/models/{modelID}/predictions` with header `Prefer: wait` (so
  the call blocks until the prediction resolves rather than requiring a
  separate poll), JSON body, JSON response.
- `Provider.VideoModel(id)` — `provider.VideoModel`: the same
  `POST /v1/models/{modelID}/predictions` + `Prefer: wait` shape, with
  `Prompt`/`AspectRatio` nested under `input` (see ProviderOptions below).
  Only `Prompt` and `AspectRatio` are wired first-class; `Resolution` and
  `DurationSec` are not sent (pass them via `ProviderOptions` under
  whatever field name the target model expects — see the
  `applyProviderOptions` note below). Unlike `ImageModel`, a non-terminal
  `Prefer: wait` response (`"starting"`/`"processing"`) is not an error —
  see "Video polling" below.
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
- **`ImageModel`: non-`"succeeded"` status is an error**, even after the
  synchronous wait — `predictionResponse.Status != "succeeded"` returns an
  error that includes `predictionResponse.Error` when present (which
  itself may be a JSON string or an arbitrary JSON value, handled by
  `errorText`). `VideoModel` does not share this behavior — see "Video
  polling" below.
- **`Prefer: wait` has a ~60s ceiling.** Replicate holds the HTTP connection
  open for at most about 60 seconds while waiting synchronously; a model
  that's still running when the window elapses legitimately returns a
  `"starting"`/`"processing"` status rather than `"succeeded"` — slow
  models can hit this even on a correct call, so treat it as "still
  running," not "broken." `ImageModel` surfaces this as the non-`"succeeded"`
  error above; `VideoModel` instead polls (see below), since image
  generations are fast enough that erroring is acceptable but multi-minute
  video generations routinely outlast the ceiling.

### Video polling

Because a video generation can easily outlast `Prefer: wait`'s ~60s
ceiling, `VideoModel.GenerateVideos` (`providers/replicate/video.go`,
`resolvePrediction`) doesn't treat a non-terminal create response as an
error: if the create call's response comes back `"starting"` or
`"processing"`, it polls `GET /v1/predictions/{id}` — sleeping
`WithPollInterval` (default 500ms) between requests, ctx-aware so a
cancelled/expired ctx interrupts the wait — until the prediction reaches a
terminal status: `"succeeded"` (fetches `output` as usual), or
`"failed"`/`"canceled"` (an error including `predictionResponse.Error` when
present, same as `ImageModel`'s non-`"succeeded"` error). This avoids
forcing a caller to retry a slow-but-successful generation into a second,
separately-billed prediction. This mirrors `providers/luma`'s
create-then-poll discipline (`WithPollInterval`, ctx-aware `sleep`,
terminal-status switch), even though Replicate's `Prefer: wait` usually
makes the create call itself synchronous.

`VideoCall.Resolution` and `VideoCall.DurationSec` are not wired to any
first-class Replicate field — pass them via `ProviderOptions["replicate"]`
under whatever field name the target model expects (see ProviderOptions
below).
- **Error body shapes.** Replicate's error responses use either
  `{"detail":"..."}` (e.g. auth errors) or an RFC-7807-style problem object
  with `"title"`/`"detail"`; `errorMessage` in
  `providers/replicate/replicate.go` prefers `detail`, then `title`.
- **Video reuses the image path's shared helpers.** `predictionResponse`,
  `outputURLs` (string-or-array `output` normalization), and `errorText`
  are shared verbatim between `image.go` and `video.go` — no duplication
  for the response-parsing side, only the request-building side differs
  (`Prompt`/`AspectRatio` vs. image's fuller field set). Video downloads
  use a separate `fetchVideo` helper (not `internal/fetchimage`, which is
  image-specific): `MediaType` comes from the response's `Content-Type`
  header, defaulting to `"video/mp4"`.

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
- [`providers/replicate/video.go`](../../providers/replicate/video.go)
- [`providers/replicate/video_test.go`](../../providers/replicate/video_test.go)
