// Command embed shows Embed, computing an embedding vector for a string.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/openai"
)

func main() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("set OPENAI_API_KEY to run")
		return
	}

	model := openai.New().EmbeddingModel("text-embedding-3-small")

	result, err := ai.Embed(context.Background(), ai.EmbedOpts{
		Model: model,
		Value: "The quick brown fox jumps over the lazy dog.",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("dimensions: %d\n", len(result.Embedding))
	fmt.Printf("usage: %+v\n", result.Usage)
}
