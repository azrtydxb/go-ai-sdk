# Hume

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/hume/hume.go`).

Hume offers no language models through this package — it only implements
`provider.SpeechModel`, against Hume's Octave text-to-speech API. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.GenerateSpeech` call shape this provider plugs into.

```go
provider := hume.New(
	hume.WithAPIKey("..."),
)
speech := provider.SpeechModel("")
```

`WithAPIKey` defaults to `os.Getenv("HUME_API_KEY")`; `WithBaseURL` defaults
to `"https://api.hume.ai"`; `WithHTTPClient` overrides the `*http.Client`.
Auth is sent as the `X-Hume-Api-Key` header on every request.

## Capabilities

- `Provider.SpeechModel(id)` — `provider.SpeechModel`: `POST /v0/tts`, JSON
  body, JSON response with base64-encoded audio.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `TranscriptionModel`.

## Quirks

- **The model ID is accepted but has no effect on the wire request.**
  Hume's `/v0/tts` endpoint does not currently accept a model selector in
  its request body — `SpeechModel(id)`'s `id` exists purely for interface
  symmetry with other providers (see the doc comment on `SpeechModel` in
  `providers/hume/hume.go`). Passing any string, including `""`, works
  identically.
- **`Language` is silently ignored.** Hume's `/v0/tts` wire format has no
  equivalent field (see the doc comment on `speechModel` in
  `providers/hume/speech.go`).
- **Text is wrapped in a single-element `utterances` array**, not sent as
  a bare `text` field — `providers/hume/speech.go`'s `speechRequest` shape
  is `{"utterances":[{"text":...,"voice":{"name":...},"speed":...}],
  "format":{"type":...}}`. `SpeechCall.Voice`, when non-empty, becomes
  `utterances[0].voice.name`; an empty `Voice` omits the `voice` field
  entirely (Hume has no SDK-enforced default voice, unlike LMNT/
  ElevenLabs).
- **Output format mapping.** `mediaTypeForFormat` in
  `providers/hume/speech.go` maps `SpeechCall.OutputFormat`:
  - `"mp3"` or `""` → `MediaType: "audio/mpeg"`
  - `"wav"` → `MediaType: "audio/wav"`
  - `"pcm"` → `MediaType: "audio/pcm"`
  - anything else → `MediaType: "application/octet-stream"`, with the
    format string still sent through verbatim as `format.type`.
- **Response audio is base64-encoded JSON, not a raw binary body.** Unlike
  ElevenLabs/LMNT, Hume's response is `{"generations":[{"audio":"<base64>"}]}`;
  the SDK decodes `generations[0].audio` and errors if the array is empty
  or the field is blank.
- **Error body shapes.** Hume's error responses use `{"message":"..."}` or
  a fallback `{"error":"..."}`; `errorMessage` in `providers/hume/hume.go`
  tries both before falling back to the raw body.

## ProviderOptions

Verified in `providers/hume/speech_test.go`
(`TestGenerateSpeech_ProviderOptionsMerge`):

```go
_, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model: speech,
	Text:  "hello",
	Voice: "myvoice",
	ProviderOptions: map[string]any{
		"hume": map[string]any{
			// passthrough key with no typed field
			"num_generations": 2,
		},
	},
})
```

`ProviderOptions["hume"]` entries are merged top-level into the marshaled
JSON request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/hume/hume.go`).

## Source of truth

- [`providers/hume/hume.go`](../../providers/hume/hume.go)
- [`providers/hume/speech.go`](../../providers/hume/speech.go)
- [`providers/hume/speech_test.go`](../../providers/hume/speech_test.go)
