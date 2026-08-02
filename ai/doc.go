// Package ai is the high-level, provider-agnostic API for go-ai-sdk: text
// generation, structured output, tool calling, streaming, embeddings, and
// media generation, built entirely on the interfaces in package provider.
//
// Every entry point takes a context.Context and an Opts struct naming a
// provider.LanguageModel (or EmbeddingModel/ImageModel/SpeechModel/
// TranscriptionModel), and returns a typed result plus an error — nothing
// here reaches into a specific providers/* package directly, so any
// provider.LanguageModel, including one wrapped by a middleware such as
// ExtractReasoningMiddleware or TelemetryMiddleware, works uniformly:
//
//	result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
//		Model:  model, // e.g. anthropic.New().Model("claude-sonnet-5")
//		Prompt: "Why is the sky blue? Answer in one sentence.",
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	fmt.Println(result.Text)
//
// GenerateText/StreamText drive a multi-step tool-calling loop (see
// GenerateTextOpts.Tools, StopWhen, PrepareStep); GenerateObject[T]/
// StreamObject[T] decode model output into a caller-supplied Go type T,
// whose JSON Schema is derived by reflection instead of a schema library;
// Embed/EmbedMany wrap provider.EmbeddingModel with batching and retries.
// StreamText's *TextStream and StreamObject's *ObjectStream expose their
// parts as an iter.Seq, consumed with a plain for range (Go's
// range-over-func iterators, package iter in the standard library).
//
// See the package README and docs/ for the full guide set, and
// docs/architecture.md for how this package relates to provider and
// providers/*.
package ai
