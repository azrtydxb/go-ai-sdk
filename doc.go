// Package goaisdk is the module root of go-ai-sdk, an idiomatic Go port of
// the Vercel AI SDK: one provider-agnostic API for text generation, streaming,
// structured output, tool calling, embeddings, and image/speech/transcription
// across 39 providers.
//
// This root package contains no code — it exists to orient you. Start with
// [github.com/azrtydxb/go-ai-sdk/ai].
//
// # Layout
//
// The module is layered, and you generally import exactly two packages: [ai]
// for the calls, and one providers/* package to name a model.
//
//   - ai — the high-level API. GenerateText, StreamText, GenerateObject[T],
//     Embed/EmbedMany, GenerateImage/Speech/Video, Transcribe, plus tools,
//     middleware, telemetry, and the retry/error model. Provider-agnostic:
//     it reaches into no providers/* package.
//   - provider — the interfaces every provider implements (LanguageModel,
//     EmbeddingModel, ImageModel, …) and the wire types they exchange
//     (Message, ContentPart, StreamPart, Response, Usage). Implement these
//     to add a provider; import them to write middleware.
//   - providers/* — one package per provider (anthropic, openai, google,
//     bedrock, …), each returning models that satisfy provider's interfaces.
//   - agent — a reusable model+instructions+tools bundle over ai's loop, and
//     sub-agent delegation via AsTool.
//   - mcp — a Model Context Protocol client, exposing an MCP server's tools
//     as ai.Tool values via mcp.Tools.
//   - codemode — executing model-authored code against your tools.
//   - ai/aitest, provider/providertest — test doubles and a shared provider
//     conformance suite.
//
// The OpenTelemetry bridge lives in a separate module,
// github.com/azrtydxb/go-ai-sdk/contrib/otel, so the core SDK stays
// dependency-free.
//
// # Quickstart
//
//	model := anthropic.New().Model("claude-sonnet-5")
//
//	result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
//		Model:  model,
//		Prompt: "Why is the sky blue? Answer in one sentence.",
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(result.Text)
//
// Swapping providers means changing only the Model line. See the runnable
// examples on [ai] for streaming, structured output, and tool calling.
//
// # Documentation
//
// The docs/ directory in the repository holds the full guide set:
// getting-started.md, architecture.md (how ai, provider, and providers/*
// relate), core/ (one guide per capability), providers/ (one per provider),
// mcp.md, troubleshooting.md, and migrating-from-vercel-ai-sdk.md for anyone
// arriving from the TypeScript SDK.
//
// [ai]: https://pkg.go.dev/github.com/azrtydxb/go-ai-sdk/ai
package goaisdk
