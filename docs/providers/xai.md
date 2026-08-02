# xAI (Grok)

`providers/xai` talks to X.AI's OpenAI-chat-completions-compatible API —
a preset over the shared `internal/openaicompat` base.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/xai"

p := xai.New(
	xai.WithAPIKey("xai-..."),                    // defaults to os.Getenv("XAI_API_KEY")
	xai.WithBaseURL("https://api.x.ai/v1"),       // the default
	xai.WithHTTPClient(http.DefaultClient),
)

model := p.Model("grok-4")
```

`WithAPIKey` sets the standard `Authorization: Bearer <key>` header.

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)`, e.g. `p.Model("grok-4")`.
- **Tool calling** — same `Model(id)`, wired through the shared
  `openaicompat` chat-completions layer.
- **Structured output** — `NativeJSON: true` in the preset's config, so
  `json_schema` response formats are used.
- **Images** — `p.ImageModel(id)`, e.g. `p.ImageModel("grok-2-image")`.

No `EmbeddingModel`, `SpeechModel`, or `TranscriptionModel` constructor is
exposed by this preset — only `Model` and `ImageModel`.

## Quirks and notes

- **Image size is rejected.** Per the package doc comment on `ImageModel`,
  the live X.AI API rejects the `size` parameter for image generation —
  leave `provider.ImageCall.Size` empty when calling an xAI image model,
  or the request fails. This is a real API quirk, not an SDK limitation:
  the SDK will happily send `size` if you set it, X.AI just errors on it.
- Auth is a plain `Authorization: Bearer` header, same as OpenAI/Groq —
  not Azure's `api-key` header.

## ProviderOptions

Entries under `ProviderOptions["xai"]` merge into the request body the
same way as every other `openaicompat` preset:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("grok-4"),
	Prompt: "What's distinctive about Grok's training data?",
	ProviderOptions: map[string]any{
		"xai": map[string]any{
			"temperature": 0.9,  // overrides Call.Temperature
			"logprobs":    true, // passthrough key, not typed on Call
		},
	},
})
```

The same merge point applies to image calls — e.g.
`ProviderOptions["xai"]["style"]` on a `GenerateImages` call — but avoid
setting `"size"` there for the reason above; it's a raw wire passthrough
and X.AI's API rejects it regardless of whether it came from
`ImageCall.Size` or `ProviderOptions`.

## Source of truth

- [`providers/xai/xai.go`](../../providers/xai/xai.go) (`ImageModel` doc
  comment for the size quirk)
- [`internal/openaicompat/openaicompat.go`](../../internal/openaicompat/openaicompat.go)
  (`Config`)
- [`internal/openaicompat/wire.go`](../../internal/openaicompat/wire.go)
  (`applyProviderOptions`)
- [`providers/xai/xai_test.go`](../../providers/xai/xai_test.go)
  (`TestDefaults`, `TestAuthHeaderAndModelSent`)
- [`providers/xai/image_test.go`](../../providers/xai/image_test.go)
