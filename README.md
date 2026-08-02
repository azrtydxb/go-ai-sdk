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
`provider.Call.ProviderOptions`'s doc comment for the exact semantics
(full usage docs land in a future task).

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
