# Google (Gemini Developer API)

`providers/google` talks to Google's Generative Language API
(`generativelanguage.googleapis.com`) — a thin preset over the shared
`internal/geminicompat` implementation.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/google"

p := google.New(
	google.WithAPIKey("AIza..."), // defaults to os.Getenv("GOOGLE_GENERATIVE_AI_API_KEY")
	google.WithBaseURL("https://generativelanguage.googleapis.com/v1beta"), // the default
	google.WithHTTPClient(http.DefaultClient),
)

model := p.Model("gemini-2.0-flash")
```

Auth is the `x-goog-api-key` request **header** — not a Bearer token, and
not a `?key=` query parameter (`providers/google/google.go:65-68`,
`authorize`).

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)`, e.g.
  `p.Model("gemini-2.0-flash")`; goes through `geminicompat.NewLanguageModel`.
- **Tool calling** — same `Model(id)`.
- **Structured output (native JSON)** — `Capabilities().NativeJSON` is
  `true` (`internal/geminicompat/language_model.go:31-33`); `ai.GenerateObject`
  sets `Call.ResponseFormat`, which `buildGenerateContentRequest` turns into
  `generationConfig.responseMimeType: "application/json"` plus
  `generationConfig.responseSchema`.
- **Grounding citations** — response
  `candidates[0].groundingMetadata.groundingChunks[].web` entries surface as
  `provider.SourcePart`s, both non-streamed (`convertResponse`) and streamed
  (`groundingSources`, `internal/geminicompat/wire.go:503-523`). This SDK
  only decodes that response shape; there is no typed request-side field to
  enable grounding/search tools — doing so would have to go through
  `ProviderOptions["google"]` as a raw passthrough of whatever `tools` entry
  the Gemini API expects, which is not itself verified against source here.
- **Embeddings** — `p.EmbeddingModel(id)`, e.g.
  `p.EmbeddingModel("text-embedding-004")`; batches up to 100 inputs per call
  (`embeddingMaxBatchSize`, `providers/google/google.go:17`), via
  `batchEmbedContents`.
- **Image generation (Imagen)** — `p.ImageModel(id)`, e.g.
  `p.ImageModel("imagen-3.0-generate-002")`, via Imagen's `:predict`
  endpoint. Accepts `AspectRatio`, not `Size` — see
  [Media](../core/media.md#size-vs-aspectratio).

## Quirks and notes

- **Schema is sanitized before being sent.** Gemini rejects JSON Schema
  documents containing `additionalProperties` at any depth; both tool
  parameter schemas and `GenerateObject`'s response schema are run through
  `stripAdditionalProperties`, which recursively deletes that key.
  (`internal/geminicompat/wire.go:402-440`.)
- **Tool results are matched by name, not by call ID.** Gemini's
  `functionResponse` wire shape identifies which call it answers by tool
  *name* (`trp.Name`), unlike Anthropic/OpenAI's ID-based correlation —
  callers populating `provider.ToolResultPart` for a Gemini model must set
  `Name`; there's no ID fallback. (`internal/geminicompat/wire.go:345-354`,
  doc comment on `toolResultParts`.)
- **Tool-call IDs are synthesized, not provided by the API.** Gemini's
  `functionCall` parts carry no ID; both the non-streamed and streamed paths
  generate `call_<name>_<index>` themselves (streaming uses a
  stream-lifetime monotonic counter specifically to avoid ID collisions
  across SSE chunks that each start their own local index).
  (`internal/geminicompat/wire.go:486`, `internal/geminicompat/language_model.go:145-198`.)
- **Reasoning parts are not replayable.** `geminicompat` has no wire
  representation for thinking/reasoning content — a `provider.ReasoningPart`
  in an assistant message being replayed is silently skipped, same as
  `SourcePart`. (`internal/geminicompat/wire.go:320-322`.)
- **`tool_result` has no error slot.** `ToolResultPart.IsError` is not
  encoded on the wire; Gemini's `functionResponse` is always plain JSON
  regardless of tool success/failure. (`internal/geminicompat/wire.go:351-354`.)

## ProviderOptions

Entries under `ProviderOptions["google"]` are shallow-merged into the raw
`generateContent`/`streamGenerateContent` request body verbatim. Verified
directly against `internal/geminicompat/provideroptions_test.go`: an option
key can override an SDK-built nested field
(`generationConfig.temperature`), and a novel top-level key not otherwise
exposed by the SDK (`safetySettings`) passes straight through:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("gemini-2.0-flash"),
	Prompt: "Explain the Go scheduler in one sentence.",
	ProviderOptions: map[string]any{
		"google": map[string]any{
			"generationConfig": map[string]any{"temperature": 0.9}, // overrides Call.Temperature
			"safetySettings": []any{ // passthrough, not typed on Call
				map[string]any{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"},
			},
		},
	},
})
```

Image calls merge under the same `"google"` key against the Imagen
`:predict` body (`instances`, `parameters`); embedding calls merge against
the `embedContentRequest`/`batchEmbedContents` body (e.g.
`ProviderOptions["google"]["title"]`).

## Source of truth

- [`providers/google/google.go`](../../providers/google/google.go)
  (package doc comment, `Option`s, `x-goog-api-key` auth)
- [`internal/geminicompat/geminicompat.go`](../../internal/geminicompat/geminicompat.go)
  (`Config`)
- [`internal/geminicompat/language_model.go`](../../internal/geminicompat/language_model.go)
  (`Capabilities`, streaming, synthesized tool-call IDs)
- [`internal/geminicompat/wire.go`](../../internal/geminicompat/wire.go)
  (`buildGenerateContentRequest`, `stripAdditionalProperties`,
  `applyProviderOptions`, `groundingSources`)
- [`internal/geminicompat/provideroptions_test.go`](../../internal/geminicompat/provideroptions_test.go)
- [`providers/google/embedding_test.go`](../../providers/google/embedding_test.go),
  [`providers/google/image_test.go`](../../providers/google/image_test.go)

See also: [Media](../core/media.md) for the Size/AspectRatio split on image
calls; [Provider options](../core/provider-options.md) for the general
merge contract.
