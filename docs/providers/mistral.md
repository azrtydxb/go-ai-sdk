# Mistral

Mistral's wire format is close to OpenAI's chat-completions shape but
differs enough (`tool_choice` values, `response_format`, the `max_tokens`
field name, how stream usage is delivered) that it's a standalone
implementation rather than a reuse of `internal/openaicompat`.

```go
provider := mistral.New(
	mistral.WithAPIKey("..."),
)
model := provider.Model("mistral-large-latest")
embedder := provider.EmbeddingModel("mistral-embed")
```

`WithAPIKey` defaults to `os.Getenv("MISTRAL_API_KEY")`; `WithBaseURL`
defaults to `"https://api.mistral.ai/v1"`; `WithHTTPClient` overrides the
`*http.Client`. Auth is sent as `Authorization: Bearer <key>` on every
request (`providers/mistral/language_model.go`, `embedding.go`).

## Capabilities

- `Provider.Model(id)` — `provider.LanguageModel`: chat, streaming
  (SSE-based, `[DONE]`-terminated), tool calling, native JSON response
  mode (`Capabilities().NativeJSON: true`).
- `Provider.EmbeddingModel(id)` — `provider.EmbeddingModel` with
  `MaxBatchSize() == 32` (`providers/mistral/mistral.go`).
- No `ImageModel`, `SpeechModel`, or `TranscriptionModel`.

## Quirks

- **Schema-dropped structured output.** `convertResponseFormat` in
  `providers/mistral/wire.go` maps `provider.ResponseFormat{Type: "json"}`
  to Mistral's wire as `{"type":"json_object"}` and never sends
  `rf.Schema`:

  > Mistral has no json_schema mode: for `ResponseFormat.Type == "json"` it
  > sends `{"type":"json_object"}` and ignores any Schema — schema
  > conformance for Mistral models is enforced by the ai core's decode
  > step, not by the wire request.

  In other words, requesting a schema-constrained JSON response still
  works end-to-end (the SDK's decode step validates/parses against the
  schema client-side), but Mistral's own API never sees or enforces the
  schema — unlike Cohere, whose `response_format` does carry a `schema`
  field alongside `json_object` (`providers/cohere/wire.go`).
- **`tool_choice: "any"` means required.** Mistral's wire word for "force a
  tool call" is `"any"`, not OpenAI's `"required"` — mapped in
  `convertToolChoice` (`providers/mistral/wire.go`).
- **Tools are omitted, not just `tool_choice`, for `ToolChoiceNone`.**
  Mistral rejects a `tool_choice` field when `tools` is empty/absent, so
  `buildChatRequest` drops the entire `Tools` array (not just
  `tool_choice`) when `Call.ToolChoice.Mode == provider.ToolChoiceNone`.
- **No error slot on tool-result messages.** `trp.IsError` is not encoded
  on `RoleTool` messages — Mistral's `tool` message wire format has no
  dedicated error field, so failed and successful tool results are sent
  identically as plain JSON content (`convertMessages` in
  `providers/mistral/wire.go`).
- **Reasoning content is dropped, not replayed.** `provider.ReasoningPart`
  in an assistant message is silently skipped when building request
  messages — Mistral has no wire representation for thinking/reasoning
  blocks (`providers/mistral/wire.go`, `providers/mistral/mistral_test.go`
  asserts this: "request body contains reasoning text, want dropped").

## ProviderOptions

`ProviderOptions["mistral"]` is shallow-merged into the built JSON request,
raw wire keys only (see [Provider options](../core/provider-options.md)).
Verified in `providers/mistral/provideroptions_test.go`:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Summarize this.",
	ProviderOptions: map[string]any{
		"mistral": map[string]any{
			// overrides Call.Temperature
			"temperature": 0.9,
			// passthrough keys with no typed field
			"safe_prompt": true,
			"random_seed": 42,
		},
	},
})
```

## Source of truth

- [`providers/mistral/mistral.go`](../../providers/mistral/mistral.go)
- [`providers/mistral/language_model.go`](../../providers/mistral/language_model.go)
- [`providers/mistral/wire.go`](../../providers/mistral/wire.go)
- [`providers/mistral/embedding.go`](../../providers/mistral/embedding.go)
- [`providers/mistral/provideroptions_test.go`](../../providers/mistral/provideroptions_test.go)
