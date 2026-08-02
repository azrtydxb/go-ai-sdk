# go-ai-sdk

An idiomatic Go port of the [Vercel AI SDK](https://sdk.vercel.ai): a single,
provider-agnostic API for generating text, streaming text, generating
structured objects, calling tools, and computing embeddings against
OpenAI, Anthropic, and Google (Gemini) — with the same concepts and naming
as the TypeScript original, expressed in native Go (context, iterators,
generics) rather than mirrored line-for-line.

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

Wave 1 (v0.1) providers, by capability:

| Capability | OpenAI | Anthropic | Google (Gemini) |
|---|---|---|---|
| `GenerateText` | ✅ | ✅ | ✅ |
| `StreamText` | ✅ | ✅ | ✅ |
| Tool calling | ✅ | ✅ | ✅ |
| `GenerateObject` / `StreamObject` | ✅ native JSON | ✅ tool-mode | ✅ native JSON |
| `Embed` / `EmbedMany` | ✅ | — (no embeddings API) | ✅ |

"Native JSON" means the provider supports schema-constrained JSON output
directly; providers without it (Anthropic) get structured output for free
via an automatically injected, forced tool call — the same `GenerateObject[T]`
call works identically either way.

## Provider roadmap

Wave 1 ships in v0.1. Later waves are tracked but not yet implemented:

| Wave | Providers | Notes |
|---|---|---|
| 1 (v0.1, shipped) | OpenAI, Anthropic, Google (Gemini) | Three distinct wire formats prove the abstraction |
| 2 | Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity | Thin presets over the OpenAI-compatible base |
| 2 | Mistral, Cohere | Own APIs, full provider implementations |
| 3 | Azure OpenAI, Vertex AI, Amazon Bedrock | Platform auth; candidates for nested submodules |
| later | Image/speech/transcription providers | Requires new model capability interfaces |

See the [design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md)
for architecture, package layout, and the full decisions log.

## License

[Apache License 2.0](LICENSE).
