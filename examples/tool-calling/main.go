// Command tool-calling shows GenerateText driving a multi-step tool loop.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

// WeatherArgs is the input schema for the get_weather tool, derived via
// reflection by ai.NewTool.
type WeatherArgs struct {
	Location string `json:"location" jsonschema:"description=City and country"`
}

func getWeather(_ context.Context, args WeatherArgs) (any, error) {
	return map[string]any{"location": args.Location, "tempC": 21, "conditions": "sunny"}, nil
}

func main() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run")
		return
	}

	model := anthropic.New().Model("claude-sonnet-5")
	weatherTool := ai.NewTool("get_weather", "Get the current weather for a location", getWeather)

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "What's the weather like in Paris right now?",
		Tools:    []ai.Tool{weatherTool},
		MaxSteps: 3,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.Text)
	fmt.Printf("steps: %d, usage: %+v\n", len(result.Steps), result.Usage)
}
