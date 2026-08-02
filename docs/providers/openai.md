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

## Source of truth

- [`providers/openai/openai.go`](../../providers/openai/openai.go)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config`)
- [`internal/openaicompat/language_model.go`](../../internal/openaicompat/language_model.go)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`, `SystemFingerprint`)
- [`internal/openaicompat/provideroptions_test.go`](../../internal/openaicompat/provideroptions_test.go)
