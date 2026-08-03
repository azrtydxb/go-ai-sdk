# Deepgram

⚠ Not yet verified against the live API — implemented against the
documented wire format only (see the package doc comment in
`providers/deepgram/deepgram.go`).

Deepgram offers no language models through this package — it only
implements `provider.TranscriptionModel`, against Deepgram's `/v1/listen`
speech-to-text endpoint. See
[Media: images, speech, transcription](../core/media.md) for the
`ai.Transcribe` call shape this provider plugs into.

```go
provider := deepgram.New(
	deepgram.WithAPIKey("..."),
)
transcription := provider.TranscriptionModel("nova-3")
```

`WithAPIKey` defaults to `os.Getenv("DEEPGRAM_API_KEY")`; `WithBaseURL`
defaults to `"https://api.deepgram.com"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as the `Authorization: Token <key>` header (not
`Bearer`) on every request.

## Capabilities

- `Provider.TranscriptionModel(id)` — `provider.TranscriptionModel`:
  `POST /v1/listen`, raw audio bytes as the request body (not JSON, not
  multipart), JSON response with word-level timestamps.
- No `Model`, `EmbeddingModel`, `ImageModel`, or `SpeechModel`.

## Quirks

- **Request body is raw audio, not JSON or multipart.** Unlike every other
  provider in this SDK, `call.Audio` is sent as the literal HTTP request
  body with `Content-Type` set to `call.MediaType` (defaulting to
  `application/octet-stream` when empty) — there is no JSON envelope to
  merge provider options into.
- **`call.Prompt` is silently ignored.** Deepgram's `/v1/listen` endpoint
  has no prompt/context parameter.
- **`punctuated_word` preferred over `word`.** Each segment's `Text` uses
  the response's `punctuated_word` field (capitalized and punctuated,
  present because the SDK always sends `smart_format=true`) when
  non-empty, falling back to the raw `word` otherwise — this keeps segment
  text consistent with the formatting of the top-level `Text` field, which
  comes from the also-smart-formatted `transcript` field
  (`providers/deepgram/transcription.go`).
- **Only the first channel/alternative is used.** Deepgram's response can
  contain multiple channels and, per channel, multiple alternatives; this
  SDK always takes `Results.Channels[0].Alternatives[0]`.
- **Error body shapes.** Deepgram's error responses use
  `{"err_msg":"..."}` or a fallback `{"error":"..."}`; `errorMessage` in
  `providers/deepgram/deepgram.go` tries both before falling back to the
  raw body.

## ProviderOptions

Verified in `providers/deepgram/transcription_test.go`
(`TestTranscribe_HappyPath`, which asserts the passthrough query params
alongside the reserved ones, and
`TestTranscribe_ProviderOptionsOverrideModelQueryParam`):

```go
_, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/wav",
	ProviderOptions: map[string]any{
		"deepgram": map[string]any{
			// passthrough keys with no typed field, added as query params
			"punctuate": true,
			"tier":      "enhanced",
			// can even override the SDK-set "model" query param
			"model": "nova-2",
		},
	},
})
```

This is the documented divergence from most other providers' convention:
because the request body is raw audio (not JSON), `ProviderOptions["deepgram"]`
entries cannot be merged into a JSON body. Instead, per Deepgram's own API
design (which takes all transcription parameters as URL query params), each
entry is added as an additional query parameter (stringified with
`fmt.Sprint`), applied after the SDK's own reserved params (`model`,
`smart_format`, `language`) — so an entry with one of those keys overrides
the SDK-set value (`providers/deepgram/transcription.go`).

**Repeated query parameters.** Some Deepgram parameters (e.g. `keywords`,
`keyterm`) are meant to be repeated as multiple same-named query params
rather than sent once as a comma-separated string. A
`ProviderOptions["deepgram"]` value of type `[]any` or `[]string` is
detected and added element-by-element via `url.Values.Add` (each element
stringified with `fmt.Sprint` for `[]any`), producing one repeated `?k=v1&k=v2`
pair per element; any other value type is still treated as scalar and set
with `url.Values.Set` as before:

```go
_, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     transcription,
	Audio:     audioBytes,
	MediaType: "audio/wav",
	ProviderOptions: map[string]any{
		"deepgram": map[string]any{
			"keywords": []any{"widget", "gizmo"},
		},
	},
})
```

Verified in
`providers/deepgram/transcription_test.go`
(`TestTranscribe_ProviderOptionsRepeatedListParam`).

## Source of truth

- [`providers/deepgram/deepgram.go`](../../providers/deepgram/deepgram.go)
- [`providers/deepgram/transcription.go`](../../providers/deepgram/transcription.go)
- [`providers/deepgram/transcription_test.go`](../../providers/deepgram/transcription_test.go)
