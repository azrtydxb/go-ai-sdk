// Command transcribe shows Transcribe, converting speech audio to text.
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

	if len(os.Args) < 2 {
		fmt.Println("usage: transcribe <audio-file-path>")
		return
	}

	audioPath := os.Args[1]
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		fmt.Printf("error reading audio file: %v\n", err)
		return
	}

	model := openai.New().TranscriptionModel("whisper-1")

	result, err := ai.Transcribe(context.Background(), ai.TranscribeOpts{
		Model:     model,
		Audio:     audioData,
		MediaType: "audio/mpeg",
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println(result.Text)
}
