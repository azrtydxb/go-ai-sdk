# Cartesia

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/cartesia/cartesia.go`).

Cartesia offers no language models through this package — it only
implements `provider.SpeechModel`, against Cartesia's text-to-speech API.
See [Media: images, video, speech, transcription, translation](../core/media.md)
for the `ai.GenerateSpeech` call shape this provider plugs into.

```go
provider := cartesia.New(
	cartesia.WithAPIKey("..."),
)
speech := provider.SpeechModel("sonic-2")
```

`WithAPIKey` defaults to `os.Getenv("CARTESIA_API_KEY")`; `WithBaseURL`
defaults to `"https://api.cartesia.ai"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as `Authorization: Bearer <key>` plus a fixed
`Cartesia-Version: 2024-11-13` header on every request.

> **Note:** Cartesia's docs have historically shown both `Authorization:
> Bearer <key>` and `X-API-Key: <key>` for authentication. This SDK sends
> `Authorization: Bearer` (this is not currently configurable —
> `ProviderOptions` only affects the JSON body, not headers). If requests
> fail with an authentication error against your account/API version, check
> whether Cartesia expects `X-API-Key` instead.

## Capabilities

- `Provider.SpeechModel(id)` — `provider.SpeechModel`: `POST /tts/bytes`,
  JSON body, binary audio response (no JSON envelope).
- No `Model`, `EmbeddingModel`, `ImageModel`, or `TranscriptionModel`.

## Quirks

- **`Voice` is required.** Unlike LMNT (which substitutes a default voice)
  or Hume (which simply omits an unset voice field), an empty
  `SpeechCall.Voice` returns `"cartesia: Voice is required"` before any HTTP
  call is made — Cartesia's voice API requires an explicit voice ID.
- **Voice is nested, not a bare wire field.** `SpeechCall.Voice` becomes
  `{"mode":"id","id":"<voice>"}` under the wire request's `voice` object,
  not a flat field.
- **Output format is a discriminated union, mapped from `OutputFormat`.**
  Cartesia's `output_format` shape differs by container: `"mp3"` sends
  `{"container","sample_rate","bit_rate"}` with **no `encoding` field** at
  all (MP3 is itself a fixed encoding); `"wav"` and `"raw"` send
  `{"container","encoding","sample_rate"}` with no `bit_rate` field. Sample
  rate is always a fixed 44100 (`defaultSampleRate`); mp3 bit rate is
  always a fixed 128000 (`defaultBitRate`), both in
  `providers/cartesia/speech.go`:

  | `OutputFormat` | `container` | `encoding` | `bit_rate` | `MediaType` |
  |---|---|---|---|---|
  | `"mp3"` or `""` (default) | `"mp3"` | *(absent)* | `128000` | `audio/mpeg` |
  | `"wav"` | `"wav"` | `"pcm_s16le"` | *(absent)* | `audio/wav` |
  | anything else (e.g. `"raw"`) | passed through verbatim | `"pcm_f32le"` | *(absent)* | `application/octet-stream` |

  This per-container mapping is a best-effort default (not spelled out
  exhaustively in Cartesia's docs) — flagged the same way as the package's
  live-testing caveat.
- **Model ID is a wire field.** `SpeechModel(id)`'s `id` (e.g. `"sonic-2"`)
  is sent as the request's `model_id` field.
- **`Language` passes through directly**, `omitempty`, with no
  model-gating or nested-object rewriting.
- **Error body shape.** `{"error":"..."}` (`errorMessage` in
  `providers/cartesia/cartesia.go`), with a fallback to the raw body when
  that shape doesn't parse.

## ProviderOptions

Verified in `providers/cartesia/speech_test.go`:

```go
_, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model: speech,
	Text:  "hello",
	Voice: "sonic-voice-id",
	ProviderOptions: map[string]any{
		"cartesia": map[string]any{
			// overrides the SDK-built language
			"language": "en",
			// passthrough key with no typed field
			"add_timestamps": true,
		},
	},
})
```

`ProviderOptions["cartesia"]` entries are merged top-level into the
marshaled JSON request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/cartesia/cartesia.go`).

## Source of truth

- [`providers/cartesia/cartesia.go`](../../providers/cartesia/cartesia.go)
- [`providers/cartesia/speech.go`](../../providers/cartesia/speech.go)
- [`providers/cartesia/speech_test.go`](../../providers/cartesia/speech_test.go)
