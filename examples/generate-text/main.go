// Command generate-text shows the simplest possible GenerateText call.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run")
		return
	}

	model := anthropic.New().Model("claude-sonnet-5")

	result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		System: "You are a concise assistant.",
		Prompt: "Why is the sky blue? Answer in one sentence.",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.Text)
	fmt.Printf("usage: %+v\n", result.Usage)
}
