# go-ai-sdk

An idiomatic Go port of the [Vercel AI SDK](https://sdk.vercel.ai): a single,
provider-agnostic API for generating text, streaming text, generating
structured objects, calling tools, and computing embeddings across OpenAI,
Anthropic, Google, Groq, xAI, DeepSeek, Together, Fireworks, Cerebras,
Perplexity, Mistral, Cohere, Azure OpenAI, Vertex AI, and Amazon Bedrock —
with the same concepts and naming as the TypeScript original, expressed in
native Go (context, iterators, generics) rather than mirrored line-for-line.

**Status: v0.1.** The public API is implemented and tested end-to-end
(unit tests plus a shared provider-conformance suite), but it is young:
expect rough edges, and expect the API to move before a 1.0. See the
[design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md) for
the full rationale and roadmap.
`GenerateTextOpts.ProviderOptions` / `provider.Call.ProviderOptions` are a
provider-specific escape hatch, keyed by provider name and shallow-merged
into the request body every built-in provider sends — see
[Core features](#core-features) below and `provider.Call.ProviderOptions`'s
doc comment for the exact semantics.

## Install

```sh
go get github.com/azrtydxb/go-ai-sdk
```

Requires Go 1.26+.

## Quickstart

### Generate text

```go
package main

import (
	"context"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	model := anthropic.New().Model("claude-sonnet-5") // reads ANTHROPIC_API_KEY

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Why is the sky blue? Answer in one sentence.",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Text)
}
```

### Stream text

```go
import "github.com/azrtydxb/go-ai-sdk/provider"

stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Count from one to five.",
})
if err != nil {
	panic(err)
}

for part := range stream.Parts() {
	if delta, ok := part.(provider.TextDelta); ok {
		fmt.Print(delta.Text)
	}
}
if err := stream.Err(); err != nil {
	panic(err)
}
```

More complete, runnable examples — including tool calling
(`ai.NewTool[Args]` and a multi-step `GenerateText` loop), structured
object generation (`ai.GenerateObject[T]`), and embeddings — live in
[`examples/`](examples/). Each one is a self-contained `package main`
guarded by an API-key env check, and each is compiled by CI.

## Core features

### ProviderOptions

`provider.Call.ProviderOptions` (and the matching field on `ai.GenerateTextOpts`,
`ai.GenerateImageOpts`, `ai.GenerateSpeechOpts`, `ai.TranscribeOpts`) is a
provider-specific escape hatch: `map[string]any` keyed by provider name (the
value returned by the model's `ProviderName()` — e.g. `"anthropic"`,
`"openai"`, `"groq"`). Each provider looks up its own key and ignores the
rest; the value must itself be a `map[string]any`, shallow-merged into the
JSON request body after the SDK builds it, so option entries win over
SDK-set fields:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "Explain quantum entanglement.",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{"top_k": 5},
	},
})
```

**These keys are the provider's raw wire fields, not Vercel AI SDK option
names.** Each entry is merged verbatim into the JSON request body this SDK
sends, using the field names the provider's HTTP API actually expects
(typically `snake_case`) — not the typed, camelCase option names Vercel's AI
SDK exposes for the same setting. There is no name translation layer: a
camelCase key from a Vercel example is not recognized by the provider and
goes out as an unknown field, silently ignored (or rejected) by the API. For
example, Vercel's `anthropic.reasoningEffort` corresponds here to
`ProviderOptions: map[string]any{"anthropic": map[string]any{"reasoning_effort": ...}}`
— the wire field name, not the SDK option name.

### Reasoning / extended thinking

Reasoning ("thinking") content surfaces uniformly as `provider.ReasoningPart`
(non-streaming) or `provider.ReasoningDelta` / `provider.ReasoningEnd`
(streaming), and `result.ReasoningText` / `stream.ReasoningText()` give the
concatenated text. On Anthropic, extended thinking is enabled via
`ProviderOptions`:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  anthropicModel,
	Prompt: "How many prime numbers are there below 100?",
	ProviderOptions: map[string]any{
		"anthropic": map[string]any{
			"thinking": map[string]any{"type": "enabled", "budget_tokens": 2048},
		},
	},
})
fmt.Println(result.ReasoningText)
fmt.Println(result.Text)
```

### Middlewares

`ai.ExtractReasoningMiddleware`, `ai.SimulateStreamingMiddleware`, and
`ai.DefaultSettingsMiddleware` wrap a `provider.LanguageModel` to add
behavior without touching the underlying provider:

```go
model := ai.ExtractReasoningMiddleware(baseModel, ai.ExtractReasoningOpts{
	TagName: "think", // splits <think>...</think> spans into reasoning content
})
```

### Registry

`ai.Registry` resolves `"provider:model"` strings into concrete models,
looking up the registered provider and type-asserting it against the
capability it needs:

```go
reg := ai.NewRegistry()
reg.Register("openai", openai.New())
reg.Register("anthropic", anthropic.New())

model, err := reg.LanguageModel("anthropic:claude-sonnet-5")
```

### Loop controls

`ai.GenerateTextOpts.StopWhen`, `ai.StepCountIs`, `PrepareStep`, and
`OnStepFinish` give fine-grained control over the multi-step tool-calling
loop shared by `GenerateText` and `StreamText`:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:    model,
	Prompt:   "What's the weather in Ghent, then in Paris?",
	Tools:    []ai.Tool{weatherTool},
	StopWhen: ai.StepCountIs(5),
	OnStepFinish: func(step ai.Step) {
		fmt.Printf("step done: %d tool call(s)\n", len(step.ToolCalls))
	},
})
```

### SmoothStream

`ai.SmoothStream` re-chunks a stream's `TextDelta`s into smaller,
evenly-sized pieces (word or line granularity) for a steadier UI cadence:

```go
for part := range ai.SmoothStream(stream.Parts(), ai.SmoothOpts{Chunking: ai.ChunkingWord}) {
	// ...
}
```

### Sources

Providers that report grounding/citation sources (currently Google only,
via `groundingMetadata`) surface them as `provider.SourcePart` content:
`result.Sources` after `GenerateText`, `stream.Sources()` after draining a
`StreamText` stream.

### CosineSimilarity

`ai.CosineSimilarity(a, b []float64) (float64, error)` computes cosine
similarity between two embedding vectors — handy for comparing the output
of `ai.Embed` / `ai.EmbedMany`.

### MCP (Model Context Protocol)

Package `mcp` is a client for [MCP](https://modelcontextprotocol.io) servers,
letting `GenerateText`/`StreamText` call tools an external process exposes.
`mcp.NewClient(transport)` wraps either transport — `mcp.NewStdioTransport(cmd
[]string, env []string)` launches a child process and speaks
newline-delimited JSON-RPC over its stdin/stdout (the child's stderr is
passed through to this process's os.Stderr, so a misbehaving server's
diagnostics aren't silently swallowed), or
`mcp.NewStreamableHTTPTransport(url string, headers map[string]string)`
speaks the MCP Streamable HTTP transport — then `Client.Initialize(ctx)`
performs the handshake. `mcp.Tools(ctx, client)` lists the server's tools and
adapts each into an `ai.Tool`, ready to hand straight to `Tools:` in
`GenerateTextOpts`:

```go
transport, err := mcp.NewStdioTransport([]string{"my-mcp-server"}, nil)
if err != nil {
	panic(err)
}
client := mcp.NewClient(transport)
defer client.Close()

if err := client.Initialize(ctx); err != nil {
	panic(err)
}
tools, err := mcp.Tools(ctx, client)
if err != nil {
	panic(err)
}

result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:    model,
	Prompt:   "List the tools you have available, then use one if it helps.",
	Tools:    tools,
	MaxSteps: 3,
})
```

A tool call that comes back `IsError` from the MCP server is turned into a
Go error, so it's recorded as a failed tool call by the normal tool loop —
no special-casing needed. A complete, runnable, env-guarded version lives at
[`examples/mcp-tools`](examples/mcp-tools).

### Telemetry

`ai.TelemetryMiddleware(model, t ai.Telemetry)` wraps a `provider.LanguageModel`
so every `Generate`/`Stream` call reports a `SpanInfo` (`Operation`,
`ModelID`, `ProviderName`, `StartTime`/`EndTime`, `Usage`, `FinishReason`,
`Err`) to `t.OnSpanStart` / `t.OnSpanEnd`. This SDK ships no OpenTelemetry
dependency (stdlib-only); `Telemetry` is a minimal seam you bridge to OTel
(or anything else) yourself — start a span in `OnSpanStart`, end it in
`OnSpanEnd`:

```go
model = ai.TelemetryMiddleware(model, myOTelBridge)
```

### Stream lifecycle callbacks

`GenerateTextOpts.OnChunk`, `.OnFinish`, and `.OnError` observe a call as it
happens, in both `GenerateText` and `StreamText`:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:  model,
	Prompt: "...",
	OnChunk:  func(part provider.StreamPart) { /* StreamText only */ },
	OnFinish: func(result *ai.GenerateTextResult) { /* fires on success */ },
	OnError:  func(err error) { /* fires on failure */ },
})
```

### Tool-call repair and active tools

`GenerateTextOpts.ActiveTools` narrows which of `Tools` are offered to the
model and executable for a given call (`nil` means all are active).
`GenerateTextOpts.RepairToolCall` gets one retry at fixing a tool call that
failed to validate — unknown name, or bad arguments — before the normal
error path (abort / record the error) takes over:

```go
result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:       model,
	Prompt:      "...",
	Tools:       []ai.Tool{weatherTool, searchTool},
	ActiveTools: []string{"get_weather"}, // searchTool offered but not usable
	RepairToolCall: func(ctx context.Context, call ai.ToolCallRecord, toolErr error) (ai.ToolCallRecord, bool) {
		// inspect toolErr, fix call.Args, return (call, true) to retry once
		return call, false
	},
})
```

### File attachments

`provider.FilePart{Data, MediaType, Filename}` adds file attachments to a
user message, alongside `TextPart`/`ImagePart`. Support varies by provider
(see the `FilePart` doc comment in `provider/message.go` for the
authoritative source):

| Provider(s) | Support |
|---|---|
| anthropic | `application/pdf` only |
| google, vertex | any `MediaType` |
| openai + OpenAI-compatible presets (azure, cerebras, deepseek, fireworks, groq, perplexity, together, xai) | `application/pdf` only |
| bedrock | a fixed set of document types recognized from `MediaType` (PDF, CSV, HTML, plain text, Markdown, Word, Excel) |
| cohere, mistral | unsupported — returns an error |

### ProviderMetadata

`provider.Response.ProviderMetadata map[string]any` is the response-side
counterpart to `ProviderOptions`, namespaced by provider name, `nil` when a
provider reports nothing extra. It's reachable from a `LanguageModel`'s raw
`Generate` result, or, after a `GenerateText`/`StreamText` call, from
`ai.Step.Response` (each `result.Steps[i].Response`):

```go
lastStep := result.Steps[len(result.Steps)-1]
if meta, ok := lastStep.Response.ProviderMetadata["anthropic"].(map[string]any); ok {
	fmt.Println(meta["cache_creation_input_tokens"])
}
```

Populated today by `anthropic` (`cache_creation_input_tokens`, when
non-zero) and every `openaicompat`-based provider (`system_fingerprint`,
when present).

## Beyond text

### Generate images

```go
model := openai.New().ImageModel("gpt-image-1")

result, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
	Model:  model,
	Prompt: "A serene landscape with mountains and a clear blue sky",
	N:      1,
	Size:   "1024x1024",
})
if err != nil {
	panic(err)
}

os.WriteFile("out.png", result.Image.Data, 0o644)
```

### Generate speech

```go
model := openai.New().SpeechModel("gpt-4o-mini-tts")

result, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
	Model:        model,
	Text:         "Hello world!",
	Voice:        "alloy",
	OutputFormat: "mp3",
})
if err != nil {
	panic(err)
}

os.WriteFile("out.mp3", result.Audio, 0o644)
```

### Transcribe audio

```go
audioData, _ := os.ReadFile("input.mp3")
model := openai.New().TranscriptionModel("whisper-1")

result, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
	Model:     model,
	Audio:     audioData,
	MediaType: "audio/mpeg",
})
if err != nil {
	panic(err)
}

fmt.Println(result.Text)
```

## Media capabilities

Supported providers for image generation, speech synthesis, and transcription:

| Capability | OpenAI | Google | Vertex AI | xAI | ElevenLabs | Groq |
|---|---|---|---|---|---|---|
| `GenerateImage` | ✅ gpt-image-1 | ✅ imagen-3.0-generate-002 | ✅ imagen-3.0-generate-002 | ✅ grok-2-image | — | — |
| `GenerateSpeech` | ✅ gpt-4o-mini-tts | — | — | — | ✅ eleven_multilingual_v2 | — |
| `Transcribe` | ✅ whisper-1 | — | — | — | ✅ scribe_v1 | ✅ whisper-large-v3-turbo |

**Note:** Other Vercel-supported media providers (Fal, Replicate, Luma, Deepgram, LMNT, Hume) are not yet included. The provider interface makes them straightforward follow-ups.

## Features

All supported providers, by capability:

| Capability | OpenAI | Anthropic | Google | Groq | xAI | DeepSeek³ | Together | Fireworks | Cerebras | Perplexity¹ | Mistral² | Cohere | Azure | Vertex AI | Bedrock |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `GenerateText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `StreamText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Tool calling | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ | ✅ | ✅ | ✅ |
| `GenerateObject` / `StreamObject` | ✅ native | ✅ tool-mode | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ tool-mode⁴ |
| `Embed` / `EmbedMany` | ✅ | — | ✅ | — | — | — | ✅ | ✅ | — | — | ✅ | ✅ | ✅ | ✅ | ✅ |

**Structured output notes:**
- "Native" means the provider supports schema-constrained JSON output directly via native JSON mode.
- "Tool-mode" (Anthropic, Bedrock) uses an automatically injected, forced tool call — the same `GenerateObject[T]` call works identically either way.
- ¹ Perplexity: no tool-calling support in the live API; `Tools` in a `Call` are serialized but may be rejected or ignored.
- ² Mistral: `GenerateObject` uses `json_object` mode only; schema is not sent on the wire but enforced by the core-side decode step.
- ³ DeepSeek: `GenerateObject` uses `json_object` mode only (DeepSeek rejects `json_schema`); schema is not sent on the wire but enforced by the core-side decode step.
- ⁴ Bedrock: the Converse API has no schema-constrained JSON response mode (`Capabilities().NativeJSON` is `false`); `GenerateObject` falls back to a forced tool call, same as Anthropic.

## Provider roadmap

Wave 1, wave 2, and wave 3 are all shipped. Later waves are tracked but not yet implemented:

| Wave | Providers | Status |
|---|---|---|
| 1 | OpenAI, Anthropic, Google (Gemini) | Shipped — three distinct wire formats prove the abstraction |
| 2 (shipped) | Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| 2 (shipped) | Mistral, Cohere | Own APIs, full provider implementations |
| 3 (shipped) | Azure OpenAI, Vertex AI, Amazon Bedrock | Platform auth: Azure (API-key preset over the OpenAI-compatible base), Vertex AI (Google service-account/ADC auth), Bedrock (AWS SigV4 request signing) |
| later | Image/speech/transcription providers | Out of scope for v1 — requires new model capability interfaces |

See the [design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md)
for architecture, package layout, and the full decisions log.

## License

[Apache License 2.0](LICENSE).
