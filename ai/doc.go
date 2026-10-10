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
// # Layout
//
// The package is one API on purpose: ai.* is the public entry point for
// every capability, and the files cluster by capability rather than by
// package. The clusters, with the narrow shared plumbing between them:
//
//   - Text generation/streaming: generate_text.go, stream_text.go, plus
//     smooth.go and timeout.go.
//   - Structured output: output.go, generate_object.go, stream_object.go,
//     middleware_json.go.
//   - Media: generate_image.go, generate_speech.go, generate_video.go,
//     transcribe.go, stream_transcribe.go, translate.go, upload_file.go.
//   - Embed/rerank: embed.go, rerank.go, similarity.go.
//   - Cross-cutting: options.go (Opts structs + buildCall), middleware.go,
//     telemetry.go, registry.go, tool.go, errors.go, approval.go,
//     runtime_context.go, partial_tracker.go.
//
// The only genuinely load-bearing coupling is ~300 lines of shared
// helpers — buildCall, partialTracker/repairPartial, translateRetryErr,
// RuntimeContextFrom — which every capability's paths converge on. Those
// helpers are why the code lives in one package: splitting ai/text,
// ai/object, ai/media, ... would hoist them into an internal package
// without removing any coupling, while breaking every import of ai.*.
//
// See the package README and docs/ for the full guide set, and
// docs/architecture.md for how this package relates to provider and
// providers/*.
package ai
