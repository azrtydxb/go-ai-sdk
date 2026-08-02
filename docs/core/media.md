# Media: images, speech, transcription

`ai.GenerateImage`, `ai.GenerateSpeech`, and `ai.Transcribe` wrap
`provider.ImageModel`, `provider.SpeechModel`, and
`provider.TranscriptionModel` respectively, all with the same shape as
`ai.GenerateText`: a `nil` `Model` returns `ai.ErrModelRequired`, each has
its own required-field error, and every call goes through the standard
retry wrapper (`MaxRetries`, default 2 — see
[Errors and retries](errors-and-retries.md)).

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

OpenAI and xAI both go through the shared `openaicompat` base and its
`images/generations` wire format, which has no aspect-ratio parameter; a
non-empty `AspectRatio` returns `"<provider>: aspect ratio is not
supported; use Size"`. Google and Vertex both go through the shared
`geminicompat` base and Imagen's `:predict` wire format, which has no size
parameter; a non-empty `Size` returns `"<provider>: size is not supported;
use AspectRatio"`.

`Seed` is silently ignored by OpenAI/xAI (the images API has no seed
parameter) but is sent through by Google/Vertex.

`N` (image count) defaults to 1 for Google/Vertex when left at 0; OpenAI/xAI
pass `N` through as-is (omitted from the wire request when 0, which the API
then defaults itself).

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

Both providers require a voice; when `Voice` is left empty, the SDK
substitutes the default above rather than sending an empty value.

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

| Provider | Response shape | Segments | Notes |
|---|---|---|---|
| OpenAI | `verbose_json` (whisper-1 and similar), `json` (gpt-4o-\* models) | Only with `verbose_json` | Models whose ID contains `"gpt-4o"` reject `verbose_json`, so those get plain `json` (text only, no `Segments`/`Language`/`DurationSec`); everything else gets `verbose_json`. |
| Groq | Same `openaicompat` base as OpenAI | Same rule as OpenAI | Groq's transcription models go through the identical wire format and `gpt-4o` substring check. |
| ElevenLabs | word-level timestamps | Synthesized from `type == "word"` entries | `DurationSec` is derived as the last segment's `EndSec` (ElevenLabs doesn't report a duration field directly); `Language` comes from `language_code`. |

All three upload `Audio` as a multipart file part; `MediaType` selects the
upload filename's extension for OpenAI/Groq (`audio/mpeg`→`mp3`,
`audio/wav`→`wav`, `audio/mp4`→`mp4`, `audio/webm`→`webm`, anything else→
`bin`) and is otherwise sent through as-is as the part's `Content-Type`
(defaulting to `application/octet-stream` when empty).

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

## Source of truth

- [`ai/generate_image.go`](../../ai/generate_image.go),
  [`ai/generate_speech.go`](../../ai/generate_speech.go),
  [`ai/transcribe.go`](../../ai/transcribe.go)
- [`provider/image.go`](../../provider/image.go),
  [`provider/speech.go`](../../provider/speech.go),
  [`provider/transcription.go`](../../provider/transcription.go)
- [`internal/openaicompat/image.go`](../../internal/openaicompat/image.go),
  [`internal/geminicompat/image.go`](../../internal/geminicompat/image.go)
- [`internal/openaicompat/speech.go`](../../internal/openaicompat/speech.go),
  [`providers/elevenlabs/speech.go`](../../providers/elevenlabs/speech.go)
- [`internal/openaicompat/transcription.go`](../../internal/openaicompat/transcription.go),
  [`providers/elevenlabs/transcription.go`](../../providers/elevenlabs/transcription.go)
- [`provider/message.go`](../../provider/message.go) (`FilePart` doc
  comment)
- [`providers/bedrock/wire.go`](../../providers/bedrock/wire.go)
  (`documentFormat`)

See also: [Provider options](provider-options.md) for passing raw
provider-specific parameters to any of these calls.
