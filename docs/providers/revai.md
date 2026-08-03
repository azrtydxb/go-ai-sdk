# Rev.ai

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/revai/revai.go`).

Rev.ai offers no language models through this package — it only implements
`provider.TranscriptionModel`, against Rev.ai's asynchronous transcription
flow: create a job from the uploaded audio, poll the job until it reaches a
terminal state, then fetch the structured transcript. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.Transcribe` call shape this provider plugs into.

```go
provider := revai.New(
	revai.WithAPIKey("..."),
)
transcription := provider.TranscriptionModel("")
```

`WithAPIKey` defaults to `os.Getenv("REVAI_API_KEY")`, falling back to
`os.Getenv("REV_AI_API_KEY")` when that's empty; `WithBaseURL` defaults to
`"https://api.rev.ai"`; `WithHTTPClient` overrides the `*http.Client`;
`WithPollInterval` overrides the interval between job status polls (default
500ms — primarily a test hook so fixtures can poll fast). Auth is sent as
the standard `Authorization: Bearer <key>` header on every request.

Rev.ai's speech-to-text API is not versioned by model id, so the `id`
passed to `TranscriptionModel` is accepted for interface conformance but
otherwise unused — pass `""`.

## Capabilities

- `Provider.TranscriptionModel(id)` — `provider.TranscriptionModel`:
  `POST /speechtotext/v1/jobs` (multipart: a `media` file part plus an
  `options` JSON part carrying `{"language":...}`) → poll
  `GET /speechtotext/v1/jobs/{id}` until `status` is `"transcribed"` or
  `"failed"` → `GET /speechtotext/v1/jobs/{id}/transcript` (with
  `Accept: application/vnd.rev.transcript.v1.0+json` for the structured
  JSON shape) to fetch the result.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `SpeechModel`.

## Quirks

- **Three-step asynchronous flow.** Every `Transcribe` call makes at least
  three HTTP requests (create, poll, fetch transcript). The poll loop
  checks immediately on entry (a job may already be transcribed by the
  time the client asks) and only sleeps `WithPollInterval`'s duration
  between subsequent attempts; the sleep is ctx-aware, returning
  `ctx.Err()` immediately on cancellation instead of waiting out the
  interval.
- **`call.Prompt` is silently ignored.** Rev.ai's transcription API has no
  prompt/context parameter.
- **`ProviderOptions` merges into the nested `options` JSON, not the
  top-level request.** Unlike most providers in this SDK, the job-creation
  request has no other body to merge into — the multipart `options` part
  *is* the job configuration Rev.ai reads — so
  `call.ProviderOptions["revai"]` is merged into that nested JSON object
  instead of top-level.
- **Transcript elements come in three types.** Rev.ai's structured
  transcript groups `monologues[].elements[]` by type: `"text"` (spoken
  words, carries `ts`/`end_ts` timestamps), `"punct"` (punctuation and
  whitespace, no timestamps, still concatenated into `Text`), and
  `"unknown"` (unintelligible speech, no timestamps or usable value) —
  `"unknown"`-type elements are intentionally omitted from both `Text` and
  `Segments`, since they carry nothing usable for either.
- **`DurationSec` is derived, not reported directly.** Rev.ai's transcript
  response has no top-level duration field; the SDK uses the last
  segment's `EndSec` instead (`0` if there are no `"text"`-type elements at
  all).
- **Error body shape.** Rev.ai's error responses follow RFC 7807 Problem
  Details: `{"title":"...","detail":"..."}`; `errorMessage` in
  `providers/revai/revai.go` prefers `detail` over `title`, falling back to
  the raw body when neither is present. A terminal `"failed"` job status
  instead carries a `failure_detail` string, included in the returned error
  when non-empty.

## ProviderOptions

Verified in `providers/revai/transcription_test.go`
(`TestTranscribe_ProviderOptionsMergeIntoOptions`):

```go
_, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/mpeg",
	ProviderOptions: map[string]any{
		"revai": map[string]any{
			// merged into the nested "options" multipart JSON part,
			// winning over whatever the SDK built (e.g. language)
			"custom_vocabularies": []any{},
		},
	},
})
```

`ProviderOptions["revai"]` entries are merged into the `{"language":...}`
JSON object sent as the multipart `options` part, entries from the option
map winning over whatever the SDK built
(`providers/revai/transcription.go`'s `buildJobOptions`).

## Source of truth

- [`providers/revai/revai.go`](../../providers/revai/revai.go)
- [`providers/revai/transcription.go`](../../providers/revai/transcription.go)
- [`providers/revai/transcription_test.go`](../../providers/revai/transcription_test.go)
