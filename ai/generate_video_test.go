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

func TestGenerateVideoLifecycleCallbacksSuccess(t *testing.T) {
	m := &aitest.MockVideoModel{Response: &provider.VideoResponse{
		Videos: []provider.GeneratedVideo{{Data: []byte("vid1")}},
	}}
	var startCalls int
	var startCall provider.VideoCall
	var endCalls int
	var endResp *provider.VideoResponse
	var endErr error
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{
		Model:  m,
		Prompt: "a cat running",
		OnVideoStart: func(call provider.VideoCall) {
			startCalls++
			startCall = call
		},
		OnVideoEnd: func(resp *provider.VideoResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 {
		t.Fatalf("OnVideoStart calls = %d, want 1", startCalls)
	}
	if startCall.Prompt != "a cat running" {
		t.Fatalf("OnVideoStart call = %+v, want built call", startCall)
	}
	if endCalls != 1 {
		t.Fatalf("OnVideoEnd calls = %d, want 1", endCalls)
	}
	if endErr != nil {
		t.Fatalf("OnVideoEnd err = %v, want nil", endErr)
	}
	if endResp == nil || len(endResp.Videos) != 1 {
		t.Fatalf("OnVideoEnd resp = %+v, want resp with 1 video", endResp)
	}
}

func TestGenerateVideoLifecycleCallbacksError(t *testing.T) {
	m := &aitest.MockVideoModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	var startCalls, endCalls int
	var endResp *provider.VideoResponse
	var endErr error
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{
		Model:  m,
		Prompt: "a cat running",
		OnVideoStart: func(call provider.VideoCall) {
			startCalls++
		},
		OnVideoEnd: func(resp *provider.VideoResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 {
		t.Fatalf("OnVideoStart calls = %d, want 1", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnVideoEnd calls = %d, want 1", endCalls)
	}
	if endResp != nil {
		t.Fatalf("OnVideoEnd resp = %+v, want nil", endResp)
	}
	if !errors.Is(endErr, err) {
		t.Fatalf("OnVideoEnd err = %v, want same as returned err %v", endErr, err)
	}
}

func TestGenerateVideoEmptyVideos(t *testing.T) {
	m := &aitest.MockVideoModel{Response: &provider.VideoResponse{Videos: []provider.GeneratedVideo{}}}
	_, err := GenerateVideo(t.Context(), GenerateVideoOpts{Model: m, Prompt: "a cat"})
	if err == nil {
		t.Fatal("want error when model returns no videos")
	}
}
