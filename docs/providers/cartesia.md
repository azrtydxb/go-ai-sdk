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
- **Output format is a nested object, mapped from `OutputFormat`.**
  `SpeechCall.OutputFormat` selects both the `output_format.container` and
  a fixed `output_format.encoding` per container, always at a fixed 44100
  sample rate (`defaultSampleRate` in `providers/cartesia/speech.go`):

  | `OutputFormat` | `container` | `encoding` | `MediaType` |
  |---|---|---|---|
  | `"mp3"` or `""` (default) | `"mp3"` | `"mp3"` | `audio/mpeg` |
  | `"wav"` | `"wav"` | `"pcm_s16le"` | `audio/wav` |
  | anything else | passed through verbatim | `"pcm_f32le"` | `application/octet-stream` |

  This encoding-per-container mapping is a best-effort default (not spelled
  out per-container in Cartesia's docs beyond "mp3"/"pcm_f32le") — flagged
  the same way as the package's live-testing caveat.
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
