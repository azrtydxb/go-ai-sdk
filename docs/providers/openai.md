# OpenAI

`providers/openai` talks to OpenAI's Chat Completions, Embeddings, Images,
Speech, and Transcription APIs directly — the full preset over the shared
`internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/openai"

p := openai.New(
	openai.WithAPIKey("sk-..."),           // defaults to os.Getenv("OPENAI_API_KEY")
	openai.WithBaseURL("https://api.openai.com/v1"), // the default; override for proxies
	openai.WithHTTPClient(http.DefaultClient),
)

model := p.Model("gpt-4o")
```

`WithAPIKey` sets the `Authorization: Bearer <key>` header. `WithBaseURL`
lets you point at a compatible proxy without losing the OpenAI preset's
defaults (`NativeJSON: true`, 2048-item embedding batch cap).

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)` → `provider.LanguageModel`,
  e.g. `p.Model("gpt-4o")`.
- **Tool calling** — same `Model(id)`; tools are wired through the shared
  `openaicompat` chat-completions request/response layer.
- **Structured output** — `Model(id)` with `Capabilities().NativeJSON ==
  true`, so `ResponseFormat` requests use `json_schema`, not just
  `json_object`.
- **Embeddings** — `p.EmbeddingModel(id)`, e.g.
  `p.EmbeddingModel("text-embedding-3-small")`; batches up to 2048 inputs
  per call.
- **Images** — `p.ImageModel(id)`, e.g. `p.ImageModel("gpt-image-1")` or
  `p.ImageModel("dall-e-3")`.
- **Speech (text-to-speech)** — `p.SpeechModel(id)`, e.g.
  `p.SpeechModel("gpt-4o-mini-tts")` or `p.SpeechModel("tts-1")`.
- **Transcription** — `p.TranscriptionModel(id)`, e.g.
  `p.TranscriptionModel("gpt-4o-transcribe")` or
  `p.TranscriptionModel("whisper-1")`.
- **Streaming (live) transcription** — `p.StreamingTranscriptionModel(id)`,
  over OpenAI's Realtime API in transcription-only mode. See
  [Realtime transcription and voice session](#realtime-transcription-and-voice-session)
  below.
- **Translation** — `p.TranslationModel(id)`, e.g.
  `p.TranslationModel("whisper-1")`; always produces English text,
  regardless of the source audio's language. See
  [Translation](#translation) below.
- **Realtime voice session** — `p.RealtimeSession(ctx, cfg)`, a live
  bidirectional voice/text conversation over the same Realtime API. See
  [Realtime transcription and voice session](#realtime-transcription-and-voice-session)
  below.
- **Files** — `p.Files()` → `provider.FileStore`, for uploading files to
  reference from a later prompt via `provider.FilePart.FileID`. See
  [Files](#files) below.

## Quirks and notes

- Auth is the standard `Authorization: Bearer` header — no
  `Config.APIKeyHeader` override (unlike Azure's `api-key` header).
- `MaxTokensParam` is left unset, so `Call.MaxTokens` is sent under
  `max_completion_tokens`, OpenAI's current field name.
- Responses that include a `system_fingerprint` surface it under
  `Response.ProviderMetadata["openai"]["system_fingerprint"]` — see
  [Provider options](../core/provider-options.md#providermetadata).

## ProviderOptions

Entries under `ProviderOptions["openai"]` are shallow-merged into the raw
chat-completions request body verbatim (raw wire key names, no
translation). Two request-shape behaviors are exercised directly against
`openaicompat`'s shared test suite: an option key can override an
SDK-built field (e.g. `temperature`), and a novel key not otherwise
exposed by the SDK passes straight through (e.g. `logprobs`):

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("gpt-4o"),
	Prompt: "Explain the Go scheduler in one sentence.",
	ProviderOptions: map[string]any{
		"openai": map[string]any{
			"temperature": 0.9,   // overrides Call.Temperature
			"logprobs":    true,  // passthrough key, not typed on Call
		},
	},
})
```

The same merge point backs image, speech, transcription, and embedding
calls — e.g. `ProviderOptions["openai"]["style"]` on an image call, or
`ProviderOptions["openai"]["encoding_format"]` on an embedding call.

## Translation

`p.TranslationModel(id)` (e.g. `"whisper-1"`) implements
`provider.TranslationModel` via
`internal/openaicompat.NewTranslationModel`: a multipart `POST
{base}/audio/translations` (field `file`, `model`, `prompt` when
non-empty), always sent with `response_format=verbose_json` — the
translations endpoint has no `gpt-4o-transcribe`-style restriction the way
`Transcribe` does, so `verbose_json` is unconditional. `ProviderOptions["openai"]`
entries are merged in as extra multipart fields. See
[Media § Translate](../core/media.md#translate).

## Files

`p.Files()` returns a `provider.FileStore`: `UploadFile` is a multipart
`POST {base}/files` (field `file`, field `purpose` defaulting to
`"user_data"` when empty), parsing `{"id","filename","bytes"}`;
`DeleteFile` is `DELETE {base}/files/{id}`. Both use the provider's
`Authorization: Bearer` header directly — no beta header, unlike
Anthropic's Files API. Not wired into `ai.Registry`; call `.Files()`
directly. See [Media § Files & skills](../core/media.md#files--skills).

⚠ **Not yet verified against the real OpenAI Files API** — tested only
against an `httptest` fixture server, same caveat as the rest of this
page's non-chat endpoints (see the live-testing note below).

## Realtime transcription and voice session

Both features dial OpenAI's Realtime API over the `internal/websocket`
client, deriving the `wss://` dial URL from the provider's configured
`baseURL` (never hardcoded), with headers `Authorization: Bearer <key>`
and `OpenAI-Beta: realtime=v1`.

⚠ **Neither has been verified against the real OpenAI Realtime endpoint**
— both are implemented and tested strictly against OpenAI's documented
event shapes, replayed by a fixture WebSocket server
(`internal/websocket/websockettest`). This is the highest-priority gap on
this page for live verification — a live-only WebSocket protocol can't be
fixture-verified with full confidence.

### Streaming transcription

`p.StreamingTranscriptionModel(id)` dials
`wss://<host>/realtime?intent=transcription` and, once open, sends a
`transcription_session.update` message with `input_audio_format` (always
`"pcm16"` today), `input_audio_transcription.model`/`.language`, and
`ProviderOptions["openai"]` merged in (options win on a key collision).
`Send` base64-encodes audio into `input_audio_buffer.append`; `CloseSend`
sends `input_audio_buffer.commit` (idempotent). `...delta`/`...completed`
events map to interim/final `TranscriptEvent`s; an `error` event ends the
stream via `Err()`. See
[Media § StreamTranscribe](../core/media.md#streamtranscribe).

### Realtime voice session

`p.RealtimeSession(ctx, openai.RealtimeConfig{...})` dials
`wss://<host>/realtime?model=<cfg.Model>` and sends a `session.update` with
`voice`/`instructions`/`input_audio_format`/`output_audio_format` (omitted
when empty) plus `ProviderOptions["openai"]` merged in. `SendAudio`/
`CommitAudio`/`SendText`/`CreateResponse` map to
`input_audio_buffer.append`/`.commit`, `conversation.item.create`, and
`response.create`. `RealtimeEvent.AudioDelta`/`.TextDelta` are populated
for both the old and new delta event names (`response.audio.delta` /
`response.output_audio.delta`, and three text-delta variants);
**`error` events do not end the session** — they surface as an ordinary
event, diverging deliberately from `StreamTranscribe`'s "error ends the
stream" rule. `RealtimeSession` is **OpenAI-only**: there is no generic
`provider.RealtimeModel` interface, and it is not wired into `ai.Registry`
— construct it directly against a `*openai.Provider`. See
[Media § Realtime voice session](../core/media.md#realtime-voice-session-openai-only).

## Source of truth

- [`providers/openai/openai.go`](../../providers/openai/openai.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`, `SystemFingerprint`)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
- [`internal/openaicompat/translation.go`](../../internal/openaicompat/translation.go)
  (`NewTranslationModel`)
- [`providers/openai/files.go`](../../providers/openai/files.go),
  [`providers/openai/files_test.go`](../../providers/openai/files_test.go)
- [`providers/openai/realtime_transcription.go`](../../providers/openai/realtime_transcription.go)
- [`providers/openai/realtime_transcription_test.go`](../../providers/openai/realtime_transcription_test.go)
- [`providers/openai/realtime.go`](../../providers/openai/realtime.go)
  (`RealtimeSession`)
- [`providers/openai/realtime_test.go`](../../providers/openai/realtime_test.go)
