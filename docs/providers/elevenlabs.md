# ElevenLabs

ElevenLabs offers no language models — the package doc comment in
`providers/elevenlabs/elevenlabs.go` states this directly — so this
provider only implements `provider.SpeechModel` (text-to-speech) and
`provider.TranscriptionModel` (speech-to-text). See
[Media: images, speech, transcription](../core/media.md) for the
`ai.GenerateSpeech`/`ai.Transcribe` call shapes this provider plugs into.

```go
provider := elevenlabs.New(
	elevenlabs.WithAPIKey("..."),
)
speech := provider.SpeechModel("eleven_multilingual_v2")
transcription := provider.TranscriptionModel("scribe_v1")
```

`WithAPIKey` defaults to `os.Getenv("ELEVENLABS_API_KEY")`; `WithBaseURL`
defaults to `"https://api.elevenlabs.io"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as the `xi-api-key` header (not
`Authorization: Bearer`) on every request.

## Capabilities

- `Provider.SpeechModel(id)` — `provider.SpeechModel`:
  `POST /v1/text-to-speech/{voice}`, JSON body, binary audio response.
- `Provider.TranscriptionModel(id)` — `provider.TranscriptionModel`:
  `POST /v1/speech-to-text`, multipart upload, word-level timestamps in the
  response.
- No `Model`, `EmbeddingModel`, or `ImageModel`.

## Quirks

- **Default voice.** An empty `SpeechCall.Voice` falls back to
  `21m00Tcm4TlvDq8ikWAM` ("Rachel"), ElevenLabs' documented default voice
  ID (`defaultVoiceID` in `providers/elevenlabs/speech.go`).
- **Output format mapping.** `outputFormatWire` in
  `providers/elevenlabs/speech.go` maps `SpeechCall.OutputFormat`:
  - `"mp3"` or `""` → wire `mp3_44100_128`, `MediaType: "audio/mpeg"`
  - `"pcm"` → wire `pcm_44100`, `MediaType: "audio/pcm"`
  - `"ulaw"` → wire `ulaw_8000`, `MediaType: "audio/basic"`
  - anything else is passed through verbatim as the `output_format` query
    parameter, with `MediaType` reported as `"application/octet-stream"`
    since the SDK doesn't know what MIME type an arbitrary format string
    maps to.
- **`Language` → `language_code`, model-gated.** `SpeechCall.Language` is
  sent as `language_code`, but per the doc comment on `speechModel` in
  `providers/elevenlabs/speech.go`, ElevenLabs only accepts this parameter
  for turbo/flash v2.5 models (e.g. `eleven_turbo_v2_5`,
  `eleven_flash_v2_5`) — other model IDs may reject it server-side. The SDK
  sends it unconditionally regardless of `modelID`; the model-gating is
  enforced by ElevenLabs' API, not by this package.
- **`Speed` maps to a nested `voice_settings.speed`.** `SpeechCall.Speed`,
  when non-nil, becomes `{"voice_settings":{"speed": *Speed}}` — no other
  `voice_settings` fields (e.g. `stability`) are set by the SDK; add them
  via `ProviderOptions["elevenlabs"]["voice_settings"]` if needed (note
  that provider options overwrite `voice_settings` wholesale, not
  field-by-field — see below).
- **Transcription language segments.** Only word entries with
  `Type == "word"` are converted to `provider.TranscriptSegment`
  (`providers/elevenlabs/transcription.go`); other word-array entry types
  from the API are skipped. `DurationSec` is derived as the last segment's
  `EndSec` — ElevenLabs' transcription response has no explicit duration
  field. `Language` comes from the response's `language_code`, not from
  the request's `Language` hint.
- **Two different provider-options merge points.** Speech is a JSON body,
  so `ProviderOptions["elevenlabs"]` is shallow-merged into the marshaled
  request (entries win over SDK-built fields, including `voice_settings`
  and `model_id`). Transcription is a multipart upload, so
  `ProviderOptions["elevenlabs"]` entries are instead written as additional
  form fields, each stringified with `fmt.Sprint`
  (`applyProviderOptionsForm` in `providers/elevenlabs/elevenlabs.go`).

## ProviderOptions

Verified in `providers/elevenlabs/provideroptions_test.go`:

```go
// Speech: JSON-body merge — options win over SDK-built fields wholesale.
speed := 1.0
_, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model: speech,
	Text:  "hello",
	Voice: "myvoice",
	Speed: &speed,
	ProviderOptions: map[string]any{
		"elevenlabs": map[string]any{
			// overrides the SDK-built voice_settings entirely
			"voice_settings": map[string]any{"speed": 2.0, "stability": 0.3},
			// overrides the model ID sent on the wire
			"model_id": "eleven_turbo_v2_5",
			// passthrough key with no typed field
			"seed": 42,
		},
	},
})
```

```go
// Transcription: multipart form field, not a JSON merge.
_, err = ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/mpeg",
	ProviderOptions: map[string]any{
		"elevenlabs": map[string]any{
			"tag_audio_events": false,
		},
	},
})
```

## Source of truth

- [`providers/elevenlabs/elevenlabs.go`](../../providers/elevenlabs/elevenlabs.go)
- [`providers/elevenlabs/speech.go`](../../providers/elevenlabs/speech.go)
- [`providers/elevenlabs/transcription.go`](../../providers/elevenlabs/transcription.go)
- [`providers/elevenlabs/provideroptions_test.go`](../../providers/elevenlabs/provideroptions_test.go)
