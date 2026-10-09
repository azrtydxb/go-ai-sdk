// Command self-hosted-voice shows speech and transcription against a
// self-hosted OpenAI-compatible server (vLLM, speaches, ...) serving
// MediaTek Breeze models. Set VOICE_BASE_URL (for example
// http://localhost:8000/v1) to run.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/providers/openaicompatible"
)

func main() {
	base := os.Getenv("VOICE_BASE_URL")
	if base == "" {
		fmt.Println("set VOICE_BASE_URL to run")
		return
	}
	ctx := context.Background()
	p := openaicompatible.New(base, openaicompatible.WithAPIKey(os.Getenv("VOICE_API_KEY")))

	speech, err := ai.GenerateSpeech(ctx, ai.GenerateSpeechOpts{
		Model:        p.Speech("MediaTek-Research/BreezyVoice"),
		Text:         "你好，歡迎使用語音合成。",
		Voice:        os.Getenv("VOICE_NAME"),
		OutputFormat: "wav",
	})
	if err != nil {
		fmt.Println("speech error:", err)
		return
	}

	tr, err := ai.Transcribe(ctx, ai.TranscribeOpts{
		Model:     p.Transcription("MediaTek-Research/Breeze-ASR-25"),
		Audio:     speech.Audio,
		MediaType: speech.MediaType,
		Language:  "zh",
	})
	if err != nil {
		fmt.Println("transcription error:", err)
		return
	}
	fmt.Println(tr.Text)
}
