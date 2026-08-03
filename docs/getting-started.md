# Getting started

## Install

```sh
go get github.com/azrtydxb/go-ai-sdk@latest
```

`go-ai-sdk` requires Go 1.26 or later and has **zero third-party
dependencies** — the module's `go.mod` declares nothing beyond the module
path and Go version.

## Your first call

Every provider package exposes a `New(...Option) *Provider`, and every
`Provider` exposes `Model(id string) provider.LanguageModel`. Pass that
model to `ai.GenerateText`:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

func main() {
	model := openai.New().Model("gpt-4o")

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Write a haiku about Go generics.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
```

`openai.New()` with no options reads the API key from the `OPENAI_API_KEY`
environment variable. Swap providers by swapping the model — the rest of the
call is identical:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	model := anthropic.New().Model("claude-sonnet-5")

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Write a haiku about Go generics.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
```

Every provider's `New` also accepts `With...` options (e.g.
`openai.WithAPIKey(...)`, `openai.WithBaseURL(...)`) to override the
environment-derived defaults — see each [provider page](providers/README.md)
for its full option set.

## Environment variables

Every provider falls back to an environment variable when its corresponding
`With...` option isn't passed to `New`. This is the full list, one row per
provider package:

| Provider | Package | Environment variable(s) | Notes |
|---|---|---|---|
| OpenAI | `providers/openai` | `OPENAI_API_KEY` | |
| Anthropic | `providers/anthropic` | `ANTHROPIC_API_KEY` | |
| Google (Gemini API) | `providers/google` | `GOOGLE_GENERATIVE_AI_API_KEY` | |
| Vertex AI | `providers/vertex` | `GOOGLE_VERTEX_PROJECT`, `GOOGLE_VERTEX_LOCATION`, `GOOGLE_APPLICATION_CREDENTIALS` | `GOOGLE_VERTEX_LOCATION` defaults to `us-central1` if unset; `GOOGLE_APPLICATION_CREDENTIALS` points at a service-account JSON file used for auto-discovered credentials |
| Azure OpenAI | `providers/azure` | `AZURE_API_KEY`, `AZURE_RESOURCE_NAME` | `AZURE_RESOURCE_NAME` is ignored when `WithBaseURL` is also given |
| Amazon Bedrock | `providers/bedrock` | `AWS_REGION` (or `AWS_DEFAULT_REGION`), `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` | region falls back to `us-east-1` if neither AWS region var is set |
| Groq | `providers/groq` | `GROQ_API_KEY` | |
| xAI | `providers/xai` | `XAI_API_KEY` | |
| DeepSeek | `providers/deepseek` | `DEEPSEEK_API_KEY` | |
| Cerebras | `providers/cerebras` | `CEREBRAS_API_KEY` | |
| Together AI | `providers/together` | `TOGETHER_AI_API_KEY` | |
| Fireworks | `providers/fireworks` | `FIREWORKS_API_KEY` | |
| Perplexity | `providers/perplexity` | `PERPLEXITY_API_KEY` | |
| Mistral | `providers/mistral` | `MISTRAL_API_KEY` | |
| Cohere | `providers/cohere` | `COHERE_API_KEY` | |
| ElevenLabs | `providers/elevenlabs` | `ELEVENLABS_API_KEY` | |
| fal | `providers/fal` | `FAL_API_KEY` (falls back to `FAL_KEY`) | |
| Replicate | `providers/replicate` | `REPLICATE_API_TOKEN` | |
| Luma | `providers/luma` | `LUMA_API_KEY` | |
| Deepgram | `providers/deepgram` | `DEEPGRAM_API_KEY` | |
| LMNT | `providers/lmnt` | `LMNT_API_KEY` | |
| Hume | `providers/hume` | `HUME_API_KEY` | |

## Streaming quickstart

`ai.StreamText` returns a `*TextStream` whose `Parts()` method is a
single-use `iter.Seq[provider.StreamPart]` — range over it, then check
`Err()` once iteration ends:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

func main() {
	model := openai.New().Model("gpt-4o")

	stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Count from 1 to 5.",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer stream.Close()

	for part := range stream.Parts() {
		if delta, ok := part.(provider.TextDelta); ok {
			fmt.Print(delta.Text)
		}
	}
	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}
}
```

`defer stream.Close()` is always safe to add — `Close` is idempotent and a
no-op once `Parts()` has run to completion, since iteration already closes
the underlying provider stream itself. See [Streaming](core/streaming.md)
for the full `StreamPart` reference.

## Where to go next

- [Generating text](core/generating-text.md) for the full `GenerateTextOpts`
  reference, the multi-step tool loop, and conversation continuation.
- [Tools](core/tools.md) to give the model callable Go functions.
- [Structured output](core/structured-output.md) to decode model output
  straight into a Go struct.
- [Provider overview](providers/README.md) for the full capability matrix
  and per-provider option reference.

## Source of truth

- [`go.mod`](../go.mod)
- [`ai/generate_text.go`](../ai/generate_text.go), [`ai/stream_text.go`](../ai/stream_text.go)
- [`providers/openai/openai.go`](../providers/openai/openai.go), [`providers/anthropic/anthropic.go`](../providers/anthropic/anthropic.go)
- Each provider's `New()` in `providers/*/`.
