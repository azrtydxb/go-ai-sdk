// Command stream-text shows StreamText, printing text deltas as they arrive.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run")
		return
	}

	model := anthropic.New().Model("claude-sonnet-5")

	stream, err := ai.StreamText(context.Background(), ai.GenerateTextOpts{
		Model:  model,
		Prompt: "Count from one to five, one number per line.",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	for part := range stream.Parts() {
		if delta, ok := part.(provider.TextDelta); ok {
			fmt.Print(delta.Text)
		}
	}
	fmt.Println()

	if err := stream.Err(); err != nil {
		fmt.Println("stream error:", err)
		return
	}
	fmt.Printf("usage: %+v\n", stream.Usage())
}
