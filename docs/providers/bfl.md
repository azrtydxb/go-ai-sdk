# Black Forest Labs

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/bfl/bfl.go`).

Black Forest Labs (BFL) offers no language models through this package — it
only implements `provider.ImageModel`, against BFL's asynchronous
image-generation API: a generation is created, then polled at the absolute
`polling_url` it returns until it reaches a terminal state. See
[Media: images, video, speech, transcription, translation](../core/media.md)
for the `ai.GenerateImage` call shape this provider plugs into.

```go
provider := bfl.New(
	bfl.WithAPIKey("..."),
)
image := provider.ImageModel("flux-pro-1.1")
```

`WithAPIKey` defaults to `os.Getenv("BFL_API_KEY")`; `WithBaseURL` defaults
to `"https://api.bfl.ai"`; `WithHTTPClient` overrides the `*http.Client`
used for both the generation/poll requests and the final sample-image
download. Auth is sent as the `x-key: <key>` header — **not** `Authorization:
Bearer`, unlike most other providers in this SDK.

`WithPollInterval(d time.Duration)` overrides the interval between
generation-status polls (default 500ms), the same test-hook pattern as
[Luma](luma.md)'s `WithPollInterval` option.

## Capabilities

- `Provider.ImageModel(id)` — `provider.ImageModel`: `POST /v1/{modelID}`
  to create a generation, then `GET <polling_url>` (the **absolute** URL
  returned by the create response, never a path this SDK builds itself)
  polled in a loop until the status is `"Ready"` or one of a fixed set of
  failure statuses.
- No `Model`, `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **`x-key`, not `Authorization`.** Every request — create and every poll —
  carries the API key under BFL's own `x-key` header.
- **`Size` ("WxH") splits into `width`/`height`**, same convention as
  Prodia: `strings.Cut(size, "x")` into `width`/`height` (both
  `omitempty`), no aspect-ratio equivalent.
- **The poll URL is followed verbatim.** The create response's
  `polling_url` field is GETted as-is — this SDK never constructs its own
  poll path from the generation `id`. A create response with an empty
  `polling_url` is a hard error (`"bfl: response contained no polling_url"`)
  rather than a guessed fallback path.
- **Terminal states are a fixed set.** `"Ready"` is success (the response's
  `result.sample` URL is fetched via `internal/fetchimage.Fetch`);
  `{"Error","Content Moderated","Request Moderated","Task not found"}` are
  failure (`failureStatuses` in `providers/bfl/bfl.go`). Any other status
  (`"Pending"`, `"Queued"`, ...) keeps polling — BFL's full status
  vocabulary isn't fully enumerated in public docs, so an unrecognized
  status is treated as "still in progress" rather than erroring, the safer
  default for an unverified wire contract.
- **Polling is context-aware.** The poll loop's sleep between requests
  returns `ctx.Err()` immediately on cancellation rather than waiting out
  the full interval (same `sleep` helper pattern as Luma).
- **`Seed` has no wire field** — BFL's create request only accepts
  `prompt`/`width`/`height` as first-class fields in this integration; if
  the target model supports a seed parameter, reach it via
  `ProviderOptions["bfl"]`.
- **Error body shapes.** `{"error":"..."}` or `{"detail":"..."}`
  (`errorMessage` in `providers/bfl/bfl.go`), with a fallback to the raw
  body when neither shape parses.

## ProviderOptions

Verified in `providers/bfl/image_test.go`:

```go
_, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  image,
	Prompt: "a red bicycle on a white background",
	Size:   "1024x1024",
	ProviderOptions: map[string]any{
		"bfl": map[string]any{
			// overrides the SDK-built prompt
			"prompt": "overridden prompt",
			// passthrough key with no typed field
			"seed": 42,
		},
	},
})
```

`ProviderOptions["bfl"]` entries are merged top-level into the marshaled
JSON create-generation request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/bfl/bfl.go`); they have no effect on
the poll requests.

## Source of truth

- [`providers/bfl/bfl.go`](../../providers/bfl/bfl.go)
- [`providers/bfl/image.go`](../../providers/bfl/image.go)
- [`providers/bfl/image_test.go`](../../providers/bfl/image_test.go)
