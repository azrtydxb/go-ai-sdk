# Media: images, video, speech, transcription, translation

`ai.GenerateImage`, `ai.GenerateVideo`, `ai.GenerateSpeech`, `ai.Transcribe`,
and `ai.Translate` wrap `provider.ImageModel`, `provider.VideoModel`,
`provider.SpeechModel`, `provider.TranscriptionModel`, and
`provider.TranslationModel` respectively, all with the same shape as
`ai.GenerateText`: a `nil` `Model` returns `ai.ErrModelRequired`, each has
its own required-field error, and every call goes through the standard
retry wrapper (`MaxRetries`, default 2 — see
[Errors and retries](errors-and-retries.md)). `ai.StreamTranscribe`
(live, bidirectional transcription) is the one exception — see
[StreamTranscribe](#streamtranscribe) below for why it has no retry.

## GenerateImage

```go
model := openai.New().ImageModel("gpt-image-1")

result, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  model,
	Prompt: "a red bicycle on a white background",
	Size:   "1024x1024",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Image.MediaType) // first image; result.Images holds all of them
```

`GenerateImageOpts.Prompt` is required (`ai.ErrPromptRequired` if empty).
`N`, `Size`, `AspectRatio`, and `Seed` are all optional and provider-defined
when unset.

### Size vs AspectRatio

Image providers split into two families, and each family accepts only one
of `Size`/`AspectRatio` — setting the wrong one returns an error rather
than being silently ignored:

| Provider | Accepts | Rejects |
|---|---|---|
| OpenAI | `Size` (e.g. `"1024x1024"`) | `AspectRatio` |
| xAI | `Size` | `AspectRatio` |
| Google (Imagen) | `AspectRatio` (e.g. `"16:9"`) | `Size` |
| Vertex AI (Imagen) | `AspectRatio` | `Size` |
| fal | `Size` | `AspectRatio` |
| Replicate | `AspectRatio` | `Size` |
| Luma | `AspectRatio` | `Size` |
| Prodia | `Size` | (no `AspectRatio` field; simply unused) |
| Black Forest Labs | `Size` | (no `AspectRatio` field; simply unused) |

OpenAI and xAI both go through the shared `openaicompat` base and its
`images/generations` wire format, which has no aspect-ratio parameter; a
non-empty `AspectRatio` returns `"<provider>: aspect ratio is not
supported; use Size"`. Google and Vertex both go through the shared
`geminicompat` base and Imagen's `:predict` wire format, which has no size
parameter; a non-empty `Size` returns `"<provider>: size is not supported;
use AspectRatio"`. fal follows the OpenAI/xAI family (`Size` only, mapped
to `image_size`); Replicate and Luma follow the Google/Vertex family
(`AspectRatio` only, mapped to `aspect_ratio`) — see
[fal](../providers/fal.md), [Replicate](../providers/replicate.md), and
[Luma](../providers/luma.md) for the exact error strings. fal's
`image_size` accepts either enum names or an explicit size object: when
`Size` matches the SDK's `"WxH"` convention (e.g. `"1024x1024"`) it's
translated into `{"width":1024,"height":1024}`; any other value (e.g.
`"square_hd"`) is passed through verbatim as a string.

Prodia and Black Forest Labs are more permissive than the strict
accept-one/reject-the-other pairs above: both parse `Size` ("WxH") into
`width`/`height` wire fields and have no dedicated `AspectRatio` wire field
at all — a non-empty `AspectRatio` is simply **ignored**, not rejected with
an error, since neither provider's `GenerateImages` even reads that field.

`Seed` is silently ignored by OpenAI/xAI (the images API has no seed
parameter) and by Luma (Dream Machine has no seed parameter), but is sent
through by Google/Vertex, fal, and Replicate. Prodia sends `Seed` through
(`config.seed`, `*int64`); Black Forest Labs has no seed field in this
integration — reach it via `ProviderOptions["bfl"]` if the target model
accepts one.

`N` (image count) defaults to 1 for Google/Vertex when left at 0; OpenAI/xAI
pass `N` through as-is (omitted from the wire request when 0, which the API
then defaults itself). fal maps `N` to `num_images` the same way; Replicate
maps it to `num_outputs`. Luma rejects `N > 1` outright (`"luma: multiple
images per call are not supported"`) since Dream Machine's image endpoint
produces exactly one image per generation. Prodia and Black Forest Labs
have no `N` wire field at all in this integration — `ImageCall.N` is simply
never read, so every call returns exactly one image regardless of what `N`
is set to (unlike Luma, which errors rather than silently ignoring it).

## GenerateVideo

```go
model := luma.New().VideoModel("ray-2")

result, err := ai.GenerateVideo(context.Background(), ai.GenerateVideoOpts{
	Model:       model,
	Prompt:      "a drone shot flying over a mountain range at sunset",
	AspectRatio: "16:9",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Video.MediaType) // first video; result.Videos holds all of them
```

`GenerateVideoOpts.Prompt` is required (`ai.ErrPromptRequired` if empty).
`AspectRatio`, `Resolution`, and `DurationSec` are all optional and
provider-defined when unset. `provider.GeneratedVideo` carries `Data`
(downloaded video bytes), `MediaType`, and `URL` (the provider's source
URL, which may expire — download `Data` yourself if you need the bytes
past that window).

### Provider matrix

| Provider | Flow | Notes |
|---|---|---|
| [Luma](../providers/luma.md) | Asynchronous: `POST /dream-machine/v1/generations` then poll `GET .../generations/{id}` until `"completed"`/`"failed"` | `DurationSec` maps to Luma's `"5s"`-style duration string; same poll-until-terminal shape as Luma's image endpoint. |
| [fal](../providers/fal.md) | Synchronous: `POST {base}/{modelID}` | Only `Prompt` and `AspectRatio` (`"aspect_ratio"`) are first-class wire fields — fal's video model catalog has no shared field name for resolution/duration, so those are `ProviderOptions`-only. |
| [Replicate](../providers/replicate.md) | Synchronous (`Prefer: wait`): `POST /v1/models/{id}/predictions` | `Prompt`/`AspectRatio` nest under `input`, same as Replicate's image endpoint; `ProviderOptions` merges into `input` too. |

### Server-returned result-URL fetches (SSRF hardening)

Several image/video providers return a **URL** the SDK must then fetch
itself — a CDN link, or, for BFL's asynchronous flow, an absolute
`polling_url` — rather than inline bytes. Because that URL is chosen by the
remote server (and could be altered by a MITM or a compromised provider),
`internal/fetchmedia.Fetch` (used directly by BFL's polling, and wrapped by
`internal/fetchimage` for the image providers that return CDN URLs, e.g.
fal/Replicate/Luma) applies:

- **SSRF blocklist**: only `http`/`https` schemes are allowed, and any
  resolved IP that's link-local unicast/multicast (covering the
  169.254.169.254 cloud-metadata endpoint), AWS's IPv6 IMDS address
  (`fd00:ec2::254`), or the unspecified address (`0.0.0.0`/`::`) is
  rejected — checked both pre-connect and, via a pinned dial-time
  `DialContext`, at the moment of the actual TCP dial, closing the
  DNS-rebind gap where a hostname could resolve to a safe IP on the
  pre-connect check and a blocked one when the transport dials it for
  real. This is a narrow, crown-jewel-only blocklist — generic private
  ranges (`10/8`, `172.16/12`, `192.168/16`) and loopback are **not**
  blocked, since self-hosted CDNs on private networks are a legitimate
  deployment.
- **Size cap**: the response body is read with a hard ceiling (256 MiB by
  default) — a body that would exceed it fails with an error instead of
  being buffered into memory in full.
- **Connection reuse and multi-IP failover**: the pinned, dial-time-checked
  `http.RoundTripper` is cached per underlying `*http.Client` (keyed by its
  base `Transport`), so repeated fetches against the same client reuse
  pooled connections instead of dialing a fresh socket every time. If a
  hostname resolves to more than one vetted IP, the dialer tries them in
  order and fails over to the next on a dial error, rather than only ever
  attempting the first — while still rejecting the whole resolution
  up front if *any* resolved IP is blocked.
- **BFL credential scoping**: BFL's API key is attached only to poll URLs
  that share the configured base URL's registrable domain (e.g.
  `api.us1.bfl.ai` and `api.bfl.ai` both under `bfl.ai`) — a poll URL
  pointing somewhere else doesn't get the credential.

None of this changes success-path behavior against a well-behaved
provider; it only rejects responses that point at an internal/metadata
target or return an unbounded body, surfacing as an ordinary error from
`GenerateImage`/`GenerateVideo`.

All three video providers download the video bytes from the URL(s) in the
provider's response themselves — `internal/fetchimage` is image-specific
(it sniffs unrecognized content types as image formats via
`internal/imagesniff`), so each video provider package has its own small
`fetchVideo` helper instead: it takes the `MediaType` from the response's
`Content-Type` header, defaulting to `"video/mp4"` when absent, with no
byte-sniffing. A non-2xx response from that download is still surfaced as
a retryable `*ai.APICallError`, same as the generation request itself.

`ai.Registry.VideoModel(id)` resolves a `"provider:model"` string into a
`provider.VideoModel` the same way `Registry.ImageModel` does, for any
registered provider implementing the (unexported) `VideoModelProvider`
interface — today that's Luma, fal, and Replicate.

## GenerateSpeech

```go
model := openai.New().SpeechModel("gpt-4o-mini-tts")

result, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model: model,
	Text:  "Hello from go-ai-sdk.",
	Voice: "alloy",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.MediaType) // "audio/mpeg"
```

`GenerateSpeechOpts.Text` is required (`ai.ErrTextRequired` if empty).

### Voice and format defaults

| Provider | Default voice | Default `OutputFormat` | Notes |
|---|---|---|---|
| OpenAI | `"alloy"` | `"mp3"` (→ `audio/mpeg`) | `response_format` values `mp3`/`wav`/`opus`/`aac`/`flac`/`pcm` map to their matching MIME type; any other value returned by the API falls back to `audio/mpeg`. |
| ElevenLabs | voice id `21m00Tcm4TlvDq8ikWAM` ("Rachel") | `"mp3"` → `mp3_44100_128` (`audio/mpeg`) | `"pcm"` maps to `pcm_44100` (`audio/pcm`); `"ulaw"` maps to `ulaw_8000` (`audio/basic`); any other value is passed through verbatim as the `output_format` query parameter, with `MediaType` reported as `application/octet-stream`. `Language` is sent as `language_code`, which ElevenLabs only accepts for turbo/flash v2.5 models — other models may reject it server-side. |
| LMNT | `"leah"` | `"mp3"` (→ `audio/mpeg`); `"wav"` → `audio/wav`; other → `application/octet-stream` | `Language` and `Speed` pass straight through to the wire request with no rewriting. See [LMNT](../providers/lmnt.md). |
| Hume | none (an empty `Voice` omits the field rather than substituting a default) | `"mp3"` (→ `audio/mpeg`); `"wav"` → `audio/wav`; `"pcm"` → `audio/pcm`; other → `application/octet-stream` | `Language` is silently ignored — Hume's wire format has no equivalent field. Response audio is base64-encoded JSON, not a raw binary body. See [Hume](../providers/hume.md). |
| Cartesia | none — `Voice` is **required**, a hard error before any HTTP call | `"mp3"` (→ `audio/mpeg`, encoding `mp3`); `"wav"` → `audio/wav` (encoding `pcm_s16le`); other → `application/octet-stream` (encoding `pcm_f32le`), always at a fixed 44100 sample rate | Unlike every other provider on this page, an empty `Voice` is an error (`"cartesia: Voice is required"`), not a substituted default. `Language` passes straight through with no rewriting. See [Cartesia](../providers/cartesia.md). |

OpenAI, ElevenLabs, and LMNT require a voice; when `Voice` is left empty,
the SDK substitutes the default above rather than sending an empty value.
Hume is the exception: an empty `Voice` simply omits the field from the
wire request, since Hume has no SDK-enforced default. Cartesia is stricter
still: an empty `Voice` is rejected outright rather than defaulted or
omitted.

## Transcribe

```go
model := openai.New().TranscriptionModel("whisper-1")

audio, _ := os.ReadFile("meeting.mp3")
result, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     model,
	Audio:     audio,
	MediaType: "audio/mpeg",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Text)
for _, seg := range result.Segments {
	fmt.Printf("[%.1fs-%.1fs] %s\n", seg.StartSec, seg.EndSec, seg.Text)
}
```

`TranscribeOpts.Audio` is required (`ai.ErrAudioRequired` if empty).

### Provider matrix

<!-- Canonical capability matrix lives in docs/providers/README.md; README.md and this table summarize it. Update all three together. -->

| Provider | Response shape | Segments | Notes |
|---|---|---|---|
| OpenAI | `verbose_json` (whisper-1 and similar), `json` (gpt-4o-\* models) | Only with `verbose_json` | Models whose ID contains `"gpt-4o"` reject `verbose_json`, so those get plain `json` (text only, no `Segments`/`Language`/`DurationSec`); everything else gets `verbose_json`. |
| Groq | Same `openaicompat` base as OpenAI | Same rule as OpenAI | Groq's transcription models go through the identical wire format and `gpt-4o` substring check. |
| ElevenLabs | word-level timestamps | Synthesized from `type == "word"` entries | `DurationSec` is derived as the last segment's `EndSec` (ElevenLabs doesn't report a duration field directly); `Language` comes from `language_code`. |
| Deepgram | `/v1/listen` JSON response, word-level timestamps | From `results.channels[0].alternatives[0].words` | Request body is the raw audio bytes, not multipart or JSON — the only transcription provider in this SDK that doesn't upload a file part. `Text` prefers `punctuated_word` over `word`. See [Deepgram](../providers/deepgram.md). |
| AssemblyAI | Async: upload → create → poll `GET /v2/transcript/{id}` | From the poll response's `words[]` (ms → sec) | Three-request flow with `WithPollInterval`-controlled, ctx-aware polling. See [AssemblyAI](../providers/assemblyai.md). |
| Gladia | Async: upload → create → poll `GET /v2/pre-recorded/{id}` | From `result.transcription.utterances[]` (already in seconds) | Same three-request async shape as AssemblyAI; `DurationSec` comes from `result.metadata.audio_duration`. See [Gladia](../providers/gladia.md). |
| Rev.ai | Async: multipart create → poll → fetch structured transcript | From `monologues[].elements[]` where `type == "text"` | Job options are multipart, not JSON; `"unknown"`-type transcript elements (unintelligible speech) are omitted from both `Text` and `Segments`. See [Rev.ai](../providers/revai.md). |

OpenAI, Groq, ElevenLabs, AssemblyAI, and Rev.ai upload `Audio` as a
multipart file part (Gladia does too, via a separate upload endpoint before
job creation); `MediaType` selects the upload filename's extension for
OpenAI/Groq (`audio/mpeg`→`mp3`, `audio/wav`→`wav`, `audio/mp4`→`mp4`,
`audio/webm`→`webm`, anything else→`bin`) and is otherwise sent through
as-is as the part's `Content-Type` (defaulting to `application/octet-stream`
when empty). Deepgram instead sends `Audio` as the literal request body
with `Content-Type: MediaType` (defaulting the same way). AssemblyAI,
Gladia, and Rev.ai are all asynchronous — see each provider's page for its
exact upload/create/poll endpoint sequence and error-body shape.

## StreamTranscribe

`ai.StreamTranscribe` opens a live, bidirectional transcription session —
one goroutine can `Send` audio chunks while another ranges over interim and
final transcript events:

```go
model := deepgram.New().StreamingTranscriptionModel("nova-3")

stream, err := ai.StreamTranscribe(context.Background(), ai.StreamTranscribeOpts{
	Model:     model,
	MediaType: "audio/pcm;rate=16000",
})
if err != nil {
	log.Fatal(err)
}
defer stream.Close()

go func() {
	stream.Send(context.Background(), audioChunk) // repeat per chunk
	stream.CloseSend(context.Background())         // signals end-of-audio
}()

for event := range stream.Events() {
	fmt.Println(event.Text, event.Final)
}
if err := stream.Err(); err != nil {
	log.Fatal(err)
}
```

Unlike every other call on this page, **`StreamTranscribe` has no retry
wrapper** — a live connection failing mid-stream can't be transparently
retried the way a single request/response call can. `opts.Model == nil`
still returns `ai.ErrModelRequired` before any dial is attempted.

`TranscriptionStream.Events()` is single-use (ranges over a channel closed
exactly once when the stream ends); `Err()` reports `nil` on a clean end —
either the provider closing the connection or the caller calling `Close()`
— and the terminal error (including `context.Canceled`) otherwise. `Send`
after `CloseSend`, or either after `Close`, returns a descriptive error
rather than panicking. If a consumer stops ranging over `Events()` before
the stream ends (e.g. `break`s out early) without cancelling `ctx`, calling
`Close()` still reclaims the reader goroutine — the provider's internal
event-delivery loop selects on the connection closing, not just on the
channel having a reader.

### Provider matrix

| Provider | Endpoint | Notes |
|---|---|---|
| [Deepgram](../providers/deepgram.md) | `wss://.../v1/listen` (live) | `MediaType`/`SampleRate` map to `encoding`/`sample_rate` query params (same convention as the REST `Transcribe` path); `CloseSend` sends `{"type":"CloseStream"}` (idempotent); a `Results` message with an empty transcript is skipped — including when it carries `is_final:true`. |
| [OpenAI](../providers/openai.md) | `wss://.../realtime?intent=transcription` | Sends a `transcription_session.update` on open (`input_audio_format`, `input_audio_transcription.model`/`.language`); `Send` base64-encodes audio into `input_audio_buffer.append`; `CloseSend` sends `input_audio_buffer.commit` (idempotent); `...delta`/`...completed` events map to interim/final `TranscriptEvent`s, an `error` event ends the stream via `Err()`. |

Both providers derive their `wss://` dial URL from the provider's
configured `baseURL` by swapping `http(s)://` for `ws(s)://` — never
hardcoded — so `WithBaseURL` works transparently against test fixtures the
same way it does for REST calls.

⚠ **Neither provider's live-transcription wire format has been verified
against the real API** — both are implemented and tested strictly against
the documented message shapes, replayed by an `httptest`-style WebSocket
fixture server (`internal/websocket/websockettest`). Live verification
against a real Deepgram/OpenAI realtime endpoint should happen before
relying on either in production; see each provider's page for the
"not yet verified" note.

`ai.StreamTranscribe` is not wired into `ai.Registry` — construct the
provider-specific `provider.StreamingTranscriptionModel` directly (as in
the example above) and pass it to `StreamTranscribe`.

## Translate

`ai.Translate` translates audio in any supported source language into
**English** text, regardless of the source language — unlike `Transcribe`,
which transcribes in the audio's own language:

```go
model := openai.New().TranslationModel("whisper-1")

audio, _ := os.ReadFile("french-meeting.mp3")
result, err := ai.Translate(context.Background(), ai.TranslateOpts{
	Model:     model,
	Audio:     audio,
	MediaType: "audio/mpeg",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(result.Text) // English translation, regardless of source language
```

`TranslateOpts.Audio` is required (`ai.ErrAudioRequired` if empty). OpenAI
is the only provider today: `internal/openaicompat.NewTranslationModel`
multipart-POSTs to `{base}/audio/translations` with `response_format`
always `verbose_json` (the translations endpoint has no
`gpt-4o-transcribe`-style restriction the way `Transcribe` does), returning
`Text`/`Language`/`DurationSec`. `ai.Translate` is not wired into
`ai.Registry` (a niche modality this wave) — construct
`openai.New().TranslationModel(id)` directly.

**`StreamTranslate` was not shipped this wave.** None of the providers
targeted so far expose a live/streaming audio-translation API (as opposed
to streaming *transcription*, which Deepgram and OpenAI both support — see
[StreamTranscribe](#streamtranscribe) above); `ai.Translate` covers the
REST translation use case instead. See
[Migrating from the Vercel AI SDK](../migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta)
for the full scope ruling.

## Realtime voice session (OpenAI-only)

`providers/openai` also exposes a minimal realtime voice session over the
same `internal/websocket` client, independent of `ai.StreamTranscribe`:
`(*openai.Provider).RealtimeSession` dials OpenAI's Realtime API, sends
audio/text into a live conversation, and streams back audio/text deltas:

```go
p := openai.New()
session, err := p.RealtimeSession(context.Background(), openai.RealtimeConfig{
	Model: "gpt-4o-realtime-preview",
	Voice: "alloy",
})
if err != nil {
	log.Fatal(err)
}
defer session.Close()

session.SendText(context.Background(), "Say hello in French.")
session.CreateResponse(context.Background())

for event := range session.Events() {
	if event.TextDelta != "" {
		fmt.Print(event.TextDelta)
	}
}
if err := session.Err(); err != nil {
	log.Fatal(err)
}
```

`SendAudio`/`CommitAudio`/`SendText`/`CreateResponse` map to
`input_audio_buffer.append`/`.commit`, `conversation.item.create`, and
`response.create` respectively. `RealtimeEvent.Raw` is always set (the
full server event); `AudioDelta` and `TextDelta` are populated for both the
old and new OpenAI delta event names (`response.audio.delta` /
`response.output_audio.delta`, and the three text-delta variants) —
deliberate future-proofing since OpenAI has renamed these event types
before. **Server `error` events are recorded but do not end the
session** — they surface as an ordinary `RealtimeEvent{Type: "error"}` and
iteration continues; only a socket failure, `ctx` cancellation, or
`Close()` ends `Events()`. This is the one place `RealtimeSession`
deliberately diverges from `StreamTranscribe`'s streams, where an `error`
event *is* terminal.

`RealtimeSession` is **OpenAI-only**: there is no generic
`provider.RealtimeModel` interface this wave, and it is not wired into
`ai.Registry` — construct it directly against an `*openai.Provider`, as
above. Vercel's WebRTC realtime transport is out of scope entirely (see
the migration guide); this SDK's realtime support is WebSocket-only.

⚠ **Not yet verified against a real OpenAI Realtime endpoint** — tested
only against a WebSocket fixture server. See
[OpenAI § Realtime](../providers/openai.md#realtime-transcription-and-voice-session)
for the live-testing note.

## FilePart attachment matrix

A `provider.FilePart` attaches a file to a *user* message (an assistant
message containing a `FilePart` is rejected by every provider):

```go
msg := provider.Message{
	Role: provider.RoleUser,
	Content: []provider.ContentPart{
		provider.TextPart{Text: "Summarize this document."},
		provider.FilePart{
			Data:      pdfBytes,
			MediaType: "application/pdf",
			Filename:  "report.pdf",
		},
	},
}
```

Support is provider-specific:

| Provider | Accepted `MediaType` | Wire shape |
|---|---|---|
| Anthropic | `application/pdf` only | A `"document"` content block; `Filename`, if set, becomes the block's title. |
| Google, Vertex AI (`geminicompat`) | any | Sent inline via `inlineData` — Gemini accepts PDFs, audio, and video inline. |
| OpenAI and other `openaicompat` providers | `application/pdf` only | A `"file"` content part with a `data:` URL. Only OpenAI itself is confirmed to accept this; other OpenAI-compatible servers may reject it — passthrough is the intended behavior, not a guarantee. |
| Amazon Bedrock | a fixed set, mapped to Converse document format codes (see below) | A Converse `"document"` content block. |
| Cohere, Mistral, and every other provider | none | Returns an error rather than silently dropping the attachment. |

Bedrock's fixed `MediaType` → format-code mapping (any other `MediaType`
returns an error):

| `MediaType` | Converse format code |
|---|---|
| `application/pdf` | `pdf` |
| `text/csv` | `csv` |
| `text/html` | `html` |
| `text/plain` | `txt` |
| `text/markdown` | `md` |
| `application/msword` | `doc` |
| `application/vnd.openxmlformats-officedocument.wordprocessingml.document` | `docx` |
| `application/vnd.ms-excel` | `xls` |
| `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` | `xlsx` |

### FileID and URL variants

`FilePart` also accepts `FileID` (a reference to a file already uploaded
to a provider's file store — see [Files & skills](#files--skills) below)
or `URL` (an externally-hosted file) instead of inline `Data`. **Exactly
one** of `Data`/`FileID`/`URL` must be set — a converter rejects a
`FilePart` with none set, or with more than one:

```go
provider.FilePart{FileID: info.ID} // info from ai.UploadFile, below
```

Per-family support (families not listed reject both variants, same as an
unsupported `Data` `MediaType`):

| Provider | `FileID` | `URL` |
|---|---|---|
| OpenAI and other `openaicompat` providers | `{"type":"file","file":{"file_id":...}}` | ✗ (no file-URL wire shape) |
| Anthropic | A `"document"` block with `source: {"type":"file","file_id":...}` | A `"document"` block with `source: {"type":"url","url":...}` |
| Google, Vertex AI (`geminicompat`) | ✗ (no wire shape) | A `fileData` part: `{"fileData":{"fileUri":...,"mimeType":...}}` (`mimeType` omitted when `MediaType` is empty; also accepts Gemini Files API URIs) |
| Amazon Bedrock | ✗ | ✗ (Converse's document block has no file-reference primitive) |

## Files & skills

`ai.UploadFile`/`ai.DeleteFile` wrap `provider.FileStore`, the interface a
provider's file-upload API implements — upload once, then reference the
returned ID from any later prompt via `FilePart.FileID` above:

```go
store := openai.New().Files() // or anthropic.New().Files()

info, err := ai.UploadFile(context.Background(), ai.UploadFileOpts{
	Store:    store,
	Data:     pdfBytes,
	Filename: "report.pdf",
})
if err != nil {
	log.Fatal(err)
}

msg := provider.Message{
	Role: provider.RoleUser,
	Content: []provider.ContentPart{
		provider.TextPart{Text: "Summarize this document."},
		provider.FilePart{FileID: info.ID},
	},
}
```

`UploadFileOpts.Store`/`.Data`/`.Filename` are required
(`ai.ErrStoreRequired`/`ai.ErrDataRequired`/`ai.ErrFilenameRequired`);
`ai.DeleteFile(ctx, ai.DeleteFileOpts{Store, ID})` requires `Store`/`ID`
(`ai.ErrStoreRequired`/`ai.ErrIDRequired`). Both wrap the call in the
standard retry logic (`MaxRetries`, default 2).

| Provider | `Files()` endpoint | Beta header | Notes |
|---|---|---|---|
| OpenAI | `POST /files` (multipart, field `file` + `purpose`, default `"user_data"`), `DELETE /files/{id}` | none | See [OpenAI § Files](../providers/openai.md#files). |
| Anthropic | `POST /v1/files` (multipart, field `file`), `DELETE /v1/files/{id}` | `anthropic-beta: files-api-2025-04-14` | See [Anthropic § Files and skills](../providers/anthropic.md#files-and-skills). |

`FileStore` is **not** wired into `ai.Registry` — call `.Files()` on a
constructed provider directly, as above.

**Anthropic skills** (`uploadSkill` in Vercel's terms) are a distinct,
**Anthropic-only** capability with no generic `provider` interface —
`(*anthropic.Provider).UploadSkill`/`.DeleteSkill` upload a `skill.zip` to
`POST /v1/skills` (multipart, file part `files[]`, field `display_name`)
with `anthropic-beta: skills-2025-10-02`:

```go
p := anthropic.New()
skill, err := p.UploadSkill(context.Background(), anthropic.UploadSkillCall{
	Zip:         skillZipBytes,
	DisplayName: "my-skill",
})
if err != nil {
	log.Fatal(err)
}
fmt.Println(skill.ID)
```

See [Anthropic § Files and skills](../providers/anthropic.md#files-and-skills)
for the beta-header isolation note (it's set only on `files.go`/`skills.go`
requests, never on `/v1/messages`) and the live-testing caveat.

## Source of truth

- [`ai/generate_image.go`](../../ai/generate_image.go),
  [`ai/generate_video.go`](../../ai/generate_video.go),
  [`ai/generate_speech.go`](../../ai/generate_speech.go),
  [`ai/transcribe.go`](../../ai/transcribe.go),
  [`ai/stream_transcribe.go`](../../ai/stream_transcribe.go),
  [`ai/translate.go`](../../ai/translate.go),
  [`ai/upload_file.go`](../../ai/upload_file.go)
- [`provider/image.go`](../../provider/image.go),
  [`provider/video.go`](../../provider/video.go),
  [`provider/speech.go`](../../provider/speech.go),
  [`provider/transcription.go`](../../provider/transcription.go),
  [`provider/transcription_stream.go`](../../provider/transcription_stream.go),
  [`provider/translation.go`](../../provider/translation.go),
  [`provider/files.go`](../../provider/files.go)
- [`internal/openaicompat/image.go`](../../internal/openaicompat/image.go),
  [`internal/geminicompat/image.go`](../../internal/geminicompat/image.go),
  [`providers/fal/image.go`](../../providers/fal/image.go),
  [`providers/replicate/image.go`](../../providers/replicate/image.go),
  [`providers/luma/image.go`](../../providers/luma/image.go),
  [`providers/prodia/image.go`](../../providers/prodia/image.go),
  [`providers/bfl/image.go`](../../providers/bfl/image.go)
- [`internal/fetchmedia/fetchmedia.go`](../../internal/fetchmedia/fetchmedia.go)
  (`Fetch`, `ValidateURL`, `PinnedTransport`, `SameRegistrableDomain` — the
  SSRF/size-cap guards described above), [`internal/fetchimage/fetchimage.go`](../../internal/fetchimage/fetchimage.go)
- [`providers/luma/video.go`](../../providers/luma/video.go),
  [`providers/fal/video.go`](../../providers/fal/video.go),
  [`providers/replicate/video.go`](../../providers/replicate/video.go)
- [`internal/openaicompat/speech.go`](../../internal/openaicompat/speech.go),
  [`providers/elevenlabs/speech.go`](../../providers/elevenlabs/speech.go),
  [`providers/lmnt/speech.go`](../../providers/lmnt/speech.go),
  [`providers/hume/speech.go`](../../providers/hume/speech.go),
  [`providers/cartesia/speech.go`](../../providers/cartesia/speech.go)
- [`internal/openaicompat/transcription.go`](../../internal/openaicompat/transcription.go),
  [`providers/deepgram/transcription.go`](../../providers/deepgram/transcription.go),
  [`providers/elevenlabs/transcription.go`](../../providers/elevenlabs/transcription.go),
  [`providers/assemblyai/transcription.go`](../../providers/assemblyai/transcription.go),
  [`providers/gladia/transcription.go`](../../providers/gladia/transcription.go),
  [`providers/revai/transcription.go`](../../providers/revai/transcription.go)
- [`providers/deepgram/live.go`](../../providers/deepgram/live.go),
  [`providers/openai/realtime_transcription.go`](../../providers/openai/realtime_transcription.go)
  (streaming transcription); [`providers/openai/realtime.go`](../../providers/openai/realtime.go)
  (realtime voice session); [`internal/websocket`](../../internal/websocket)
  (the underlying WebSocket client — see [Architecture](../architecture.md))
- [`internal/openaicompat/translation.go`](../../internal/openaicompat/translation.go)
- [`providers/openai/files.go`](../../providers/openai/files.go),
  [`providers/anthropic/files.go`](../../providers/anthropic/files.go),
  [`providers/anthropic/skills.go`](../../providers/anthropic/skills.go)
- [`provider/message.go`](../../provider/message.go) (`FilePart` doc
  comment)
- [`providers/bedrock/wire.go`](../../providers/bedrock/wire.go)
  (`documentFormat`)

See also: [Provider options](provider-options.md) for passing raw
provider-specific parameters to any of these calls.
