package agent_test

import (
	"context"
	"fmt"
	"log"

	"github.com/azrtydxb/go-ai-sdk/agent"
	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// The examples below use aitest.MockModel so they run offline and produce
// deterministic output. In real code the Model field holds a live model, e.g.
// anthropic.New().Model("claude-sonnet-5").

// ExampleAgent bundles model, instructions and tools once, then runs the same
// configuration repeatedly with different inputs.
func ExampleAgent() {
	model := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "Ghent is in Belgium."}},
			FinishReason: provider.FinishStop,
		},
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "Lyon is in France."}},
			FinishReason: provider.FinishStop,
		},
	}}

	geographer := &agent.Agent{
		Model:        model,
		Instructions: "Answer geography questions in one short sentence.",
		MaxSteps:     4,
	}

	for _, city := range []string{"Ghent", "Lyon"} {
		res, err := geographer.Generate(context.Background(), agent.RunOpts{
			Prompt: "Which country is " + city + " in?",
		})
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(res.Text)
	}
	// Output:
	// Ghent is in Belgium.
	// Lyon is in France.
}

// ExampleAsTool exposes one Agent to another as a callable tool. The outer
// agent decides when to delegate; the sub-agent runs its own tool loop and
// returns its final text as the tool result.
func ExampleAsTool() {
	// The sub-agent: given a city, it reports the weather.
	subModel := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "sunny, 21°C"}},
		FinishReason: provider.FinishStop,
	}}}
	weatherAgent := &agent.Agent{
		Model:        subModel,
		Instructions: "Report the weather for the given city, tersely.",
	}

	// The outer agent: calls the sub-agent, then writes the final answer.
	mainModel := &aitest.MockModel{Responses: []*provider.Response{
		{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "call_1", Name: "weather", Args: []byte(`{"prompt":"Ghent"}`),
			}},
			FinishReason: provider.FinishToolCalls,
		},
		{
			Content:      []provider.ContentPart{provider.TextPart{Text: "Pack sunglasses: sunny, 21°C."}},
			FinishReason: provider.FinishStop,
		},
	}}

	planner := &agent.Agent{
		Model:        mainModel,
		Instructions: "Help the user plan their day. Delegate weather lookups.",
		Tools: []ai.Tool{
			agent.AsTool(weatherAgent, "weather", "Get the weather for a city."),
		},
		MaxSteps: 5,
	}

	res, err := planner.Generate(context.Background(), agent.RunOpts{
		Prompt: "What should I wear in Ghent today?",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("steps:", len(res.Steps))
	fmt.Println(res.Text)
	// Output:
	// steps: 2
	// Pack sunglasses: sunny, 21°C.
}
