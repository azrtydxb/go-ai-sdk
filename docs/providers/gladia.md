# Gladia

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/gladia/gladia.go`).

Gladia offers no language models through this package — it only implements
`provider.TranscriptionModel`, against Gladia's three-endpoint asynchronous
transcription flow: upload the audio, create a pre-recorded transcription
job from the resulting URL, then poll the job until it reaches a terminal
state. See [Media: images, speech, transcription](../core/media.md) for the
`ai.Transcribe` call shape this provider plugs into.

```go
provider := gladia.New(
	gladia.WithAPIKey("..."),
)
transcription := provider.TranscriptionModel("")
```

`WithAPIKey` defaults to `os.Getenv("GLADIA_API_KEY")`; `WithBaseURL`
defaults to `"https://api.gladia.io"`; `WithHTTPClient` overrides the
`*http.Client`; `WithPollInterval` overrides the interval between job status
polls (default 500ms — primarily a test hook so fixtures can poll fast).
Auth is sent as the `x-gladia-key` header on every request.

Gladia's pre-recorded transcription API is not versioned by model id, so
the `id` passed to `TranscriptionModel` is accepted for interface
conformance but otherwise unused — pass `""`.

## Capabilities

- `Provider.TranscriptionModel(id)` — `provider.TranscriptionModel`:
  `POST /v2/upload` (multipart, `audio` field) → `POST /v2/pre-recorded`
  (create, `audio_url` + optional `language_config.languages` from
  `call.Language`) → poll `GET /v2/pre-recorded/{id}` until `status` is
  `"done"` or `"error"`.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `SpeechModel`.

## Quirks

- **Three-step asynchronous flow.** Every `Transcribe` call makes at least
  three HTTP requests (upload, create, poll). The poll loop checks
  immediately on entry (a job may already be done by the time the client
  asks) and only sleeps `WithPollInterval`'s duration between subsequent
  attempts; the sleep is ctx-aware, returning `ctx.Err()` immediately on
  cancellation instead of waiting out the interval.
- **`call.Prompt` is silently ignored.** Gladia's transcription API has no
  prompt/context parameter.
- **The create response's `result_url` is unused by design.** Gladia's
  `/v2/pre-recorded` create call returns both a job `id` and a
  `result_url`; this provider polls by `id` (`GET
  /v2/pre-recorded/{id}`) rather than fetching `result_url` directly — the
  field is decoded but never read.
- **Segments come from `utterances`, duration from `metadata`.** The
  response's `result.transcription.full_transcript` becomes `Text`, each
  `result.transcription.utterances[]` entry becomes a
  `provider.TranscriptSegment` (its `start`/`end` are already in seconds,
  no conversion needed), and `result.metadata.audio_duration` becomes
  `DurationSec`.
- **Error body shape.** Gladia's error responses use `{"message":"..."}`;
  `errorMessage` in `providers/gladia/gladia.go` parses that field, falling
  back to the raw body when it's absent or empty. A terminal `"error"`
  status instead carries an `error_code` value (of unspecified JSON type,
  decoded as `any`), included in the returned error when present.

## ProviderOptions

Verified in `providers/gladia/transcription_test.go`
(`TestTranscribe_ProviderOptionsMergeTopLevel`):

```go
_, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/wav",
	ProviderOptions: map[string]any{
		"gladia": map[string]any{
			// merged top-level into the /v2/pre-recorded create request,
			// winning over whatever the SDK built (e.g. language_config)
			"diarization": true,
		},
	},
})
```

`ProviderOptions["gladia"]` entries are merged top-level into the
already-marshaled JSON create-job request body, entries from the option map
winning over whatever the SDK built (`providers/gladia/gladia.go`'s
`applyProviderOptions`).

## Source of truth

- [`providers/gladia/gladia.go`](../../providers/gladia/gladia.go)
- [`providers/gladia/transcription.go`](../../providers/gladia/transcription.go)
- [`providers/gladia/transcription_test.go`](../../providers/gladia/transcription_test.go)
