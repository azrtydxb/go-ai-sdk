package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateVideoHappyPath(t *testing.T) {
	m := &aitest.MockVideoModel{Response: &provider.VideoResponse{
		Videos: []provider.GeneratedVideo{
			{Data: []byte("vid1"), MediaType: "video/mp4"},
			{Data: []byte("vid2"), MediaType: "video/mp4"},
		},
	}}
	res, err := GenerateVideo(t.Context(), GenerateVideoOpts{
		Model:       m,
		Prompt:      "a cat running",
		AspectRatio: "16:9",
		Resolution:  "720p",
		DurationSec: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if call.Prompt != "a cat running" || call.AspectRatio != "16:9" || call.Resolution != "720p" || call.DurationSec != 5 {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if string(res.Video.Data) != "vid1" {
		t.Fatalf("Video = %+v, want first video", res.Video)
	}
	if len(res.Videos) != 2 {
		t.Fatalf("Videos = %d, want 2", len(res.Videos))
	}
}

func TestGenerateVideoNilModel(t *testing.T) {
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{Prompt: "a cat"})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestGenerateVideoEmptyPrompt(t *testing.T) {
	m := &aitest.MockVideoModel{}
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{Model: m})
	if !errors.Is(err, ErrPromptRequired) {
		t.Fatalf("err = %v, want ErrPromptRequired", err)
	}
}

func TestGenerateVideoRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockVideoModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{Model: m, Prompt: "a cat"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}

func TestGenerateVideoEmptyVideos(t *testing.T) {
	m := &aitest.MockVideoModel{Response: &provider.VideoResponse{Videos: []provider.GeneratedVideo{}}}
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{Model: m, Prompt: "a cat"})
	if err == nil {
		t.Fatal("want error when model returns no videos")
	}
}
