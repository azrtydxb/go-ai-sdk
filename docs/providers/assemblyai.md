# AssemblyAI

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/assemblyai/assemblyai.go`).

AssemblyAI offers no language models through this package — it only
implements `provider.TranscriptionModel`, against AssemblyAI's
three-endpoint asynchronous transcription flow: upload the audio, create a
transcript from the resulting URL, then poll the transcript until it
reaches a terminal state. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.Transcribe` call shape this provider plugs into.

```go
provider := assemblyai.New(
	assemblyai.WithAPIKey("..."),
)
transcription := provider.TranscriptionModel("universal")
```

`WithAPIKey` defaults to `os.Getenv("ASSEMBLYAI_API_KEY")`; `WithBaseURL`
defaults to `"https://api.assemblyai.com"`; `WithHTTPClient` overrides the
`*http.Client`; `WithPollInterval` overrides the interval between transcript
status polls (default 500ms — primarily a test hook so fixtures can poll
fast). Auth is sent as the `authorization: <key>` header (lowercase, no
`Bearer` prefix) on every request.

An empty model id (`provider.TranscriptionModel("")`) omits the
`speech_model` field from the transcript creation request entirely, letting
AssemblyAI use its own default rather than sending an empty value.

## Capabilities

- `Provider.TranscriptionModel(id)` — `provider.TranscriptionModel`:
  `POST /v2/upload` (raw audio body) → `POST /v2/transcript` (create,
  `speech_model`/`language_code` from `id`/`call.Language`) → poll
  `GET /v2/transcript/{id}` until `status` is `"completed"` or `"error"`.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `SpeechModel`.

## Quirks

- **Three-step asynchronous flow.** Every `Transcribe` call makes at least
  three HTTP requests (upload, create, poll — usually more, since the poll
  step repeats until the transcript reaches a terminal state). The poll loop
  checks immediately on entry (a transcript may already be complete by the
  time the client asks) and only sleeps `WithPollInterval`'s duration
  between subsequent attempts; the sleep is ctx-aware, returning
  `ctx.Err()` immediately on cancellation instead of waiting out the
  interval.
- **`call.Prompt` is silently ignored.** AssemblyAI's transcription API has
  no prompt/context parameter.
- **Word timestamps are in milliseconds on the wire.** The SDK converts
  each word's `start`/`end` to seconds (`StartSec`/`EndSec`) by dividing by
  1000 when building `provider.TranscriptSegment`s.
- **Error body shape.** AssemblyAI's error responses use
  `{"error":"..."}`; `errorMessage` in `providers/assemblyai/assemblyai.go`
  parses that field, falling back to the raw body when it's absent or
  empty. The poll step applies the same fallback a second time: if a
  terminal `"error"` status response has an empty `error` field, the
  returned error includes the raw response body rather than reporting the
  failure with no detail at all.

## ProviderOptions

Verified in `providers/assemblyai/transcription_test.go`
(`TestTranscribe_ProviderOptionsMergeTopLevel`):

```go
_, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/mpeg",
	ProviderOptions: map[string]any{
		"assemblyai": map[string]any{
			// merged top-level into the /v2/transcript create request,
			// winning over whatever the SDK built (e.g. speech_model)
			"language_detection": true,
		},
	},
})
```

`ProviderOptions["assemblyai"]` entries are merged top-level into the
already-marshaled JSON create-transcript request body, entries from the
option map winning over whatever the SDK built
(`providers/assemblyai/assemblyai.go`'s `applyProviderOptions`).

## Source of truth

- [`providers/assemblyai/assemblyai.go`](../../providers/assemblyai/assemblyai.go)
- [`providers/assemblyai/transcription.go`](../../providers/assemblyai/transcription.go)
- [`providers/assemblyai/transcription_test.go`](../../providers/assemblyai/transcription_test.go)
