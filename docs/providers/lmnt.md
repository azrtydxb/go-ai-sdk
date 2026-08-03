# LMNT

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/lmnt/lmnt.go`).

LMNT offers no language models through this package — it only implements
`provider.SpeechModel`, against LMNT's text-to-speech API. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.GenerateSpeech` call shape this provider plugs into.

```go
provider := lmnt.New(
	lmnt.WithAPIKey("..."),
)
speech := provider.SpeechModel("blizzard")
```

`WithAPIKey` defaults to `os.Getenv("LMNT_API_KEY")`; `WithBaseURL` defaults
to `"https://api.lmnt.com"`; `WithHTTPClient` overrides the `*http.Client`.
Auth is sent as the `X-API-Key` header (not `Authorization`) on every
request.

## Capabilities

- `Provider.SpeechModel(id)` — `provider.SpeechModel`:
  `POST /v1/ai/speech/bytes`, JSON body, binary audio response.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `TranscriptionModel`.

## Quirks

- **Default voice.** An empty `SpeechCall.Voice` falls back to `"leah"`,
  LMNT's documented default voice (`defaultVoice` in
  `providers/lmnt/speech.go`).
- **Output format mapping is narrow.** `mediaTypeForFormat` in
  `providers/lmnt/speech.go` maps `SpeechCall.OutputFormat`:
  - `"mp3"` or `""` → `MediaType: "audio/mpeg"`
  - `"wav"` → `MediaType: "audio/wav"`
  - anything else → `MediaType: "application/octet-stream"`, with the
    format string still sent through verbatim as the wire `format` field.
- **Model ID is a wire field, not just a path/header value.** Unlike most
  providers, `SpeechModel(id)`'s `id` is sent on the wire as the request's
  `model` field (`omitempty`, so an empty model ID is simply left off the
  request).
- **`Language` and `Speed` pass through directly**, with no model-gating
  or nested-object rewriting — `SpeechCall.Language` maps straight to the
  wire `language` field and `SpeechCall.Speed` to `speed`, both
  `omitempty`/nil-omitted.
- **Error body shape.** LMNT's error responses use `{"error":"..."}`
  (`errorMessage` in `providers/lmnt/lmnt.go`), with a fallback to the raw
  body when that shape doesn't parse.

## ProviderOptions

Verified in `providers/lmnt/speech_test.go`
(`TestGenerateSpeech_ProviderOptionsMerge`):

```go
_, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model: speech,
	Text:  "hello",
	Voice: "myvoice",
	ProviderOptions: map[string]any{
		"lmnt": map[string]any{
			// overrides the SDK-built voice
			"voice": "override-voice",
			// passthrough key with no typed field
			"seed":  42,
		},
	},
})
```

`ProviderOptions["lmnt"]` entries are merged top-level into the marshaled
JSON request body, winning over whatever the SDK built
(`applyProviderOptions` in `providers/lmnt/lmnt.go`).

## Source of truth

- [`providers/lmnt/lmnt.go`](../../providers/lmnt/lmnt.go)
- [`providers/lmnt/speech.go`](../../providers/lmnt/speech.go)
- [`providers/lmnt/speech_test.go`](../../providers/lmnt/speech_test.go)
