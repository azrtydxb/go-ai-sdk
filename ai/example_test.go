package ai_test

import (
	"context"
	"fmt"
	"log"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// The examples below use aitest.MockModel so they run offline and produce
// deterministic output. In real code the Model field holds a live model, e.g.
// anthropic.New().Model("claude-sonnet-5") — nothing else about the call
// changes.

func ExampleGenerateText() {
	// A real program would use a provider model here instead:
	//	model := anthropic.New().Model("claude-sonnet-5")
	model := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "Rayleigh scattering."}},
		FinishReason: provider.FinishStop,
		Usage:        provider.Usage{InputTokens: 12, OutputTokens: 4, TotalTokens: 16},
	}}}

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		System: "Answer in one sentence.",
		Prompt: "Why is the sky blue?",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(res.Text)
	fmt.Println("tokens:", res.Usage.TotalTokens)
	// Output:
	// Rayleigh scattering.
	// tokens: 16
}

// ExampleStreamText consumes a stream as it arrives. Parts is an iter.Seq, so
// it is consumed with a plain for range; check Err after the loop, since a
// mid-stream failure can only be reported once iteration ends.
func ExampleStreamText() {
	model := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "Hel"},
		provider.TextDelta{Text: "lo, "},
		provider.TextDelta{Text: "world"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 8}},
	}}}

	stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Say hello.",
	})
	if err != nil {
		log.Fatal(err)
	}

	for part := range stream.Parts() {
		if delta, ok := part.(provider.TextDelta); ok {
			fmt.Printf("delta: %q\n", delta.Text)
		}
	}

	// Err reports any error that ended the stream early.
	if err := stream.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("accumulated:", stream.Text())
	// Output:
	// delta: "Hel"
	// delta: "lo, "
	// delta: "world"
	// accumulated: Hello, world
}

// ExampleGenerateObject decodes the model's output into a caller-supplied Go
// type. The JSON Schema sent to the provider is derived from T by reflection,
// including the json struct tags.
func ExampleGenerateObject() {
	type Forecast struct {
		City string `json:"city"`
		Temp int    `json:"temp"`
	}

	model := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"city":"Ghent","temp":21}`}},
			FinishReason: provider.FinishStop,
		}},
	}

	res, err := ai.GenerateObject[Forecast](context.Background(), ai.GenerateObjectOpts{
		Model:  model,
		Prompt: "Forecast for Ghent, in celsius.",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%s: %d°C\n", res.Object.City, res.Object.Temp)
	// Output:
	// Ghent: 21°C
}

// ExampleNewTool shows the multi-step tool-calling loop: the model asks for a
// tool, GenerateText executes it, feeds the result back, and the model answers.
// Both round trips are visible in Steps.
func ExampleNewTool() {
	type weatherArgs struct {
		City string `json:"city"`
	}

	weather := ai.NewTool("get_weather", "Look up the current weather in a city.",
		func(_ context.Context, args weatherArgs) (any, error) {
			return "sunny, 21°C", nil
		})

	model := &aitest.MockModel{Responses: []*provider.Response{
		// Step 1: the model calls the tool.
		{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "call_1", Name: "get_weather", Args: []byte(`{"city":"Ghent"}`),
			}},
			FinishReason: provider.FinishToolCalls,
		},
		// Step 2: given the tool result, the model answers.
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "It's sunny in Ghent."}},
			FinishReason: provider.FinishStop,
		},
	}}

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "What's the weather in Ghent?",
		Tools:    []ai.Tool{weather},
		MaxSteps: 5,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("steps:", len(res.Steps))
	fmt.Println("tool result:", res.Steps[0].ToolResults[0].Result)
	fmt.Println(res.Text)
	// Output:
	// steps: 2
	// tool result: sunny, 21°C
	// It's sunny in Ghent.
}

// ExampleStepCountIs stops the tool-calling loop on a condition rather than
// letting it run to MaxSteps. HasToolCall and LoopFinished compose the same way.
func ExampleStepCountIs() {
	type pingArgs struct{}

	ping := ai.NewTool("ping", "Ping.", func(_ context.Context, _ pingArgs) (any, error) {
		return "pong", nil
	})

	toolCall := func() *provider.Response {
		return &provider.Response{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c", Name: "ping", Args: []byte(`{}`),
			}},
			FinishReason: provider.FinishToolCalls,
		}
	}
	// The model would keep calling the tool forever; StopWhen cuts it off.
	model := &aitest.MockModel{Responses: []*provider.Response{
		toolCall(), toolCall(), toolCall(), toolCall(),
	}}

	res, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "Ping repeatedly.",
		Tools:    []ai.Tool{ping},
		MaxSteps: 10,
		StopWhen: ai.StepCountIs(2),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("steps:", len(res.Steps))
	// Output:
	// steps: 2
}

func ExampleEmbed() {
	// In real code: openai.New().EmbeddingModel("text-embedding-3-small")
	model := &aitest.MockEmbedder{Dim: 3}

	res, err := ai.Embed(context.Background(), ai.EmbedOpts{
		Model: model,
		Value: "go-ai-sdk",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("dimensions:", len(res.Embedding))
	// Output:
	// dimensions: 3
}

// ExampleEmbedMany embeds a slice of values, batching them according to the
// model's MaxBatchSize and preserving input order in the result.
func ExampleEmbedMany() {
	model := &aitest.MockEmbedder{Dim: 3, BatchSize: 2}

	res, err := ai.EmbedMany(context.Background(), ai.EmbedManyOpts{
		Model:  model,
		Values: []string{"alpha", "beta", "gamma"},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("embeddings:", len(res.Embeddings))
	fmt.Println("provider batches:", len(model.RecordedBatches()))
	// Output:
	// embeddings: 3
	// provider batches: 2
}

func ExampleCosineSimilarity() {
	same, err := ai.CosineSimilarity([]float64{1, 0, 0}, []float64{1, 0, 0})
	if err != nil {
		log.Fatal(err)
	}
	orthogonal, err := ai.CosineSimilarity([]float64{1, 0, 0}, []float64{0, 1, 0})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("identical:   %.1f\n", same)
	fmt.Printf("orthogonal:  %.1f\n", orthogonal)
	// Output:
	// identical:   1.0
	// orthogonal:  0.0
}
