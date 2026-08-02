# go-ai-sdk

An idiomatic Go port of the [Vercel AI SDK](https://sdk.vercel.ai): a single,
provider-agnostic API for generating text, streaming text, generating
structured objects, calling tools, and computing embeddings across OpenAI,
Anthropic, Google, Groq, xAI, DeepSeek, Together, Fireworks, Cerebras,
Perplexity, Mistral, and Cohere — with the same concepts and naming as the
TypeScript original, expressed in native Go (context, iterators, generics)
rather than mirrored line-for-line.

**Status: v0.1.** The public API is implemented and tested end-to-end
(unit tests plus a shared provider-conformance suite), but it is young:
expect rough edges, and expect the API to move before a 1.0. See the
[design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md) for
the full rationale and roadmap.
`GenerateTextOpts.ProviderOptions` / `provider.Call.ProviderOptions` exist
as a reserved escape hatch but are not yet read by any built-in provider
in v0.1 — setting them is currently a silent no-op.

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

## Features

All supported providers, by capability:

| Capability | OpenAI | Anthropic | Google | Groq | xAI | DeepSeek³ | Together | Fireworks | Cerebras | Perplexity¹ | Mistral² | Cohere |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `GenerateText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `StreamText` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Tool calling | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | ✅ | ✅ |
| `GenerateObject` / `StreamObject` | ✅ native | ✅ tool-mode | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native | ✅ native |
| `Embed` / `EmbedMany` | ✅ | — | ✅ | — | — | — | ✅ | ✅ | — | — | ✅ | ✅ |

**Structured output notes:**
- "Native" means the provider supports schema-constrained JSON output directly via native JSON mode.
- "Tool-mode" (Anthropic) uses an automatically injected, forced tool call — the same `GenerateObject[T]` call works identically either way.
- ¹ Perplexity: no tool-calling support in the live API; `Tools` in a `Call` are serialized but may be rejected or ignored.
- ² Mistral: `GenerateObject` uses `json_object` mode only; schema is not sent on the wire but enforced by the core-side decode step.
- ³ DeepSeek: `GenerateObject` uses `json_object` mode only (DeepSeek rejects `json_schema`); schema is not sent on the wire but enforced by the core-side decode step.

## Provider roadmap

Wave 1 ships in v0.1; Wave 2 is now shipped. Later waves are tracked but not yet implemented:

| Wave | Providers | Status |
|---|---|---|
| 1 | OpenAI, Anthropic, Google (Gemini) | Shipped — three distinct wire formats prove the abstraction |
| 2 (shipped) | Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| 2 (shipped) | Mistral, Cohere | Own APIs, full provider implementations |
| 3 | Azure OpenAI, Vertex AI, Amazon Bedrock | Planned — platform auth; candidates for nested submodules |
| later | Image/speech/transcription providers | Out of scope for v1 — requires new model capability interfaces |

See the [design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md)
for architecture, package layout, and the full decisions log.

## License

[Apache License 2.0](LICENSE).
