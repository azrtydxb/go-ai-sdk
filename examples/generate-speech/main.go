// Command generate-speech shows GenerateSpeech, synthesizing spoken audio from text.
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

	model := openai.New().SpeechModel("gpt-4o-mini-tts")

	result, err := ai.GenerateSpeech(context.Background(), ai.GenerateSpeechOpts{
		Model:        model,
		Text:         "Hello world! This is a test of text-to-speech synthesis.",
		Voice:        "alloy",
		OutputFormat: "mp3",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Write the audio to a file
	if err := os.WriteFile("out.mp3", result.Audio, 0o644); err != nil {
		fmt.Println("error writing file:", err)
		return
	}

	fmt.Printf("audio saved to out.mp3 (%d bytes, %s)\n", len(result.Audio), result.MediaType)
}
