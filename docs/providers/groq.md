# Groq

`providers/groq` talks to Groq's OpenAI-chat-completions-compatible API —
a preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/groq"

p := groq.New(
	groq.WithAPIKey("gsk_..."),                          // defaults to os.Getenv("GROQ_API_KEY")
	groq.WithBaseURL("https://api.groq.com/openai/v1"),  // the default
	groq.WithHTTPClient(http.DefaultClient),
)

model := p.Model("llama-3.3-70b-versatile")
```

`WithAPIKey` sets the standard `Authorization: Bearer <key>` header —
Groq uses the same auth mechanism as OpenAI, not Azure's `api-key` header.

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)`.
- **Tool calling** — same `Model(id)`, wired through the shared
  `openaicompat` chat-completions layer.
- **Structured output** — `NativeJSON: true` in the preset's config, so
  `json_schema` response formats are used.
- **Transcription** — `p.TranscriptionModel(id)`, e.g.
  `p.TranscriptionModel("whisper-large-v3")` (Groq-hosted Whisper).

No `EmbeddingModel`, `ImageModel`, or `SpeechModel` constructor is exposed
by this preset — only `Model` and `TranscriptionModel`.

## Quirks and notes

- Auth is a plain `Authorization: Bearer` header, same as OpenAI.
- No embedding or image endpoints are wired — Groq's public API doesn't
  offer them, so this preset only constructs `Model` and
  `TranscriptionModel`.
- `MaxTokensParam` is left unset (defaults to `max_completion_tokens`),
  same as OpenAI.

## ProviderOptions

Entries under `ProviderOptions["groq"]` merge into the request body the
same way as every other `openaicompat` preset:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("llama-3.3-70b-versatile"),
	Prompt: "List three uses for a Go channel.",
	ProviderOptions: map[string]any{
		"groq": map[string]any{
			"temperature": 0.9,  // overrides Call.Temperature
			"logprobs":    true, // passthrough key, not typed on Call
		},
	},
})
```

For `TranscriptionModel`, options are written as extra multipart form
fields instead of merged JSON — e.g.
`ProviderOptions["groq"]["temperature"]` becomes a `temperature` form
field alongside the audio upload.

## Source of truth

- [`providers/groq/groq.go`](../../providers/groq/groq.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config`)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`, `applyProviderOptionsForm`)
- [`providers/groq/groq_test.go`](../../providers/groq/groq_test.go)
  (`TestDefaults`, `TestAuthHeaderAndModelSent`)
- [`providers/groq/transcription_test.go`](../../providers/groq/transcription_test.go)
