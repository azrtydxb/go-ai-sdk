# Luma

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/luma/luma.go`).

Luma offers no language models through this package — it implements
`provider.ImageModel` and `provider.VideoModel`, against Luma's Dream
Machine image- and video-generation APIs. Both are asynchronous: a
generation is created, then polled until it reaches a terminal state. See
[Media: images, video, speech, transcription, translation](../core/media.md)
for the `ai.GenerateImage`/`ai.GenerateVideo` call shapes this provider
plugs into.

```go
provider := luma.New(
	luma.WithAPIKey("..."),
)
image := provider.ImageModel("photon-1")
video := provider.VideoModel("ray-2")
```

`WithAPIKey` defaults to `os.Getenv("LUMA_API_KEY")`; `WithBaseURL` defaults
to `"https://api.lumalabs.ai"`; `WithHTTPClient` overrides the `*http.Client`
used for requests and image downloads. Auth is sent as the standard
`Authorization: Bearer <key>` header.

`WithPollInterval(d time.Duration)` overrides the interval between
generation-status polls (default 500ms). This option is **Luma-specific** —
no other provider in this SDK has a poll hook, since Luma is the only
asynchronous image API implemented so far. It exists primarily as a test
hook so fixtures can poll fast; production callers generally don't need it.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`:
  `POST /dream-machine/v1/generations/image` to create a generation, then
  `GET /dream-machine/v1/generations/{id}` polled in a loop until
  `state` is `"completed"` or `"failed"`.
- `Provider.VideoModel(id)` — `provider.VideoModel`:
  `POST /dream-machine/v1/generations` (video) to create a generation, then
  the same `GET /dream-machine/v1/generations/{id}` poll loop. `DurationSec`
  maps to Luma's `"5s"`-style duration string
  (`strconv.FormatFloat(sec, 'f', -1, 64) + "s"`, so `2.5` → `"2.5s"`); a
  `"failed"` terminal state surfaces `FailureReason` in the returned error
  when present.
- No `Model`, `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **Aspect ratio only, no size, and single-image only.** A non-empty
  `ImageCall.Size` returns `"luma: size is not supported; use
  AspectRatio"`; `ImageCall.N > 1` returns `"luma: multiple images per call
  are not supported"` (`providers/luma/image.go`) — Luma's Dream Machine
  image endpoint produces exactly one image per generation.
- **`Seed` is silently ignored.** Luma's Dream Machine API has no seed
  parameter; `ImageCall.Seed` is accepted for interface symmetry but never
  sent on the wire.
- **Polling is context-aware.** The poll loop's sleep between requests
  (`sleep` in `providers/luma/image.go`) returns `ctx.Err()` immediately on
  cancellation rather than waiting out the full interval; a `"failed"`
  terminal state surfaces `generationResponse.FailureReason` in the
  returned error when present.
- **Error body shape.** Luma's error responses use `{"detail":"..."}`
  (`errorMessage` in `providers/luma/luma.go`), with a fallback to the raw
  body when that shape doesn't parse.
- **Video download is not sniffed, unlike images.** `internal/fetchimage`
  (used for image downloads) sniffs an unrecognized `Content-Type` as an
  image format via `internal/imagesniff`; video downloads use a local
  `fetchVideo` helper instead (`providers/luma/video.go`) that takes
  `MediaType` from the response's `Content-Type` header, defaulting to
  `"video/mp4"` when absent — no byte-sniffing, since there's no
  video-bytes sniffer in this codebase.

## ProviderOptions

Verified in `providers/luma/image_test.go`
(`TestGenerateImages_ProviderOptionsMergeTopLevel`):

```go
_, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  image,
	Prompt: "a red bicycle on a white background",
	ProviderOptions: map[string]any{
		"luma": map[string]any{
			// overrides the SDK-built prompt
			"prompt":       "overridden prompt",
			// passthrough key with no typed field
			"callback_url": "https://example.test/hook",
		},
	},
})
```

`ProviderOptions["luma"]` entries are merged top-level into the marshaled
JSON create-generation request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/luma/luma.go`); they have no effect
on the poll requests.

## Source of truth

- [`providers/luma/luma.go`](../../providers/luma/luma.go)
- [`providers/luma/image.go`](../../providers/luma/image.go)
- [`providers/luma/image_test.go`](../../providers/luma/image_test.go)
- [`providers/luma/video.go`](../../providers/luma/video.go)
- [`providers/luma/video_test.go`](../../providers/luma/video_test.go)
