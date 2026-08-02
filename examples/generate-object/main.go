// Command generate-object decodes a structured Recipe from the model.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

// Recipe is the target shape for GenerateObject. Anthropic has no native
// JSON mode, so this uses go-ai-sdk's automatic tool-mode structured output.
type Recipe struct {
	Name        string   `json:"name"`
	Ingredients []string `json:"ingredients"`
	Steps       []string `json:"steps"`
}

func main() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run")
		return
	}

	model := anthropic.New().Model("claude-sonnet-5")

	result, err := ai.GenerateObject[Recipe](context.Background(), ai.GenerateObjectOpts{
		Model:  model,
		Prompt: "Give me a simple recipe for guacamole.",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("%+v\n", result.Object)
	fmt.Printf("usage: %+v\n", result.Usage)
}
