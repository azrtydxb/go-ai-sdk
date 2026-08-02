// Command mcp-tools drives an MCP stdio server's tools through GenerateText.
//
// Usage: mcp-tools <server command> [args...]
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/mcp"
	"github.com/azrtydxb/go-ai-sdk/providers/anthropic"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: mcp-tools <server command> [args...]")
		return
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		fmt.Println("set ANTHROPIC_API_KEY to run")
		return
	}

	ctx := context.Background()

	transport, err := mcp.NewStdioTransport(os.Args[1:], nil)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	client := mcp.NewClient(transport)
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		fmt.Println("error:", err)
		return
	}

	tools, err := mcp.Tools(ctx, client)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	model := anthropic.New().Model("claude-sonnet-5")

	result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
		Model:    model,
		Prompt:   "List the tools you have available, then use one of them if it makes sense to.",
		Tools:    tools,
		MaxSteps: 3,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.Text)
}
