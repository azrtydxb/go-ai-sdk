// Command generate-image shows GenerateImage, creating an image from a text prompt.
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

	model := openai.New().ImageModel("gpt-image-1")

	result, err := ai.GenerateImage(context.Background(), ai.GenerateImageOpts{
		Model:  model,
		Prompt: "A serene landscape with mountains and a clear blue sky",
		N:      1,
		Size:   "1024x1024",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Write the first generated image to a file
	imageData := result.Image.Data
	if err := os.WriteFile("out.png", imageData, 0o644); err != nil {
		fmt.Println("error writing file:", err)
		return
	}

	fmt.Printf("image saved to out.png (%d bytes, %s)\n", len(imageData), result.Image.MediaType)
}
