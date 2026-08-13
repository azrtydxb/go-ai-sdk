package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateImageHappyPath(t *testing.T) {
	m := &aitest.MockImageModel{Response: &provider.ImageResponse{
		Images: []provider.GeneratedImage{
			{Data: []byte("img1"), MediaType: "image/png"},
			{Data: []byte("img2"), MediaType: "image/png"},
		},
	}}
	seed := int64(42)
	res, err := GenerateImage(t.Context(), GenerateImageOpts{
		Model:       m,
		Prompt:      "a cat",
		N:           2,
		Size:        "1024x1024",
		AspectRatio: "16:9",
		Seed:        &seed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if call.Prompt != "a cat" || call.N != 2 || call.Size != "1024x1024" || call.AspectRatio != "16:9" {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if call.Seed == nil || *call.Seed != 42 {
		t.Fatalf("call.Seed = %v, want 42", call.Seed)
	}
	if string(res.Image.Data) != "img1" {
		t.Fatalf("Image = %+v, want first image", res.Image)
	}
	if len(res.Images) != 2 {
		t.Fatalf("Images = %d, want 2", len(res.Images))
	}
}

func TestGenerateImageNilModel(t *testing.T) {
	_, err := GenerateImage(t.Context(), GenerateImageOpts{Prompt: "a cat"})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestGenerateImageEmptyPrompt(t *testing.T) {
	m := &aitest.MockImageModel{}
	_, err := GenerateImage(t.Context(), GenerateImageOpts{Model: m})
	if !errors.Is(err, ErrPromptRequired) {
		t.Fatalf("err = %v, want ErrPromptRequired", err)
	}
}

func TestGenerateImageRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockImageModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := GenerateImage(t.Context(), GenerateImageOpts{Model: m, Prompt: "a cat"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}

func TestGenerateImageLifecycleCallbacksSuccess(t *testing.T) {
	m := &aitest.MockImageModel{Response: &provider.ImageResponse{
		Images: []provider.GeneratedImage{{Data: []byte("img1")}},
	}}
	var startCalls int
	var startCall provider.ImageCall
	var endCalls int
	var endResp *provider.ImageResponse
	var endErr error
	_, err := GenerateImage(t.Context(), GenerateImageOpts{
		Model:  m,
		Prompt: "a cat",
		OnImageStart: func(call provider.ImageCall) {
			startCalls++
			startCall = call
		},
		OnImageEnd: func(resp *provider.ImageResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 {
		t.Fatalf("OnImageStart calls = %d, want 1", startCalls)
	}
	if startCall.Prompt != "a cat" {
		t.Fatalf("OnImageStart call = %+v, want built call", startCall)
	}
	if endCalls != 1 {
		t.Fatalf("OnImageEnd calls = %d, want 1", endCalls)
	}
	if endErr != nil {
		t.Fatalf("OnImageEnd err = %v, want nil", endErr)
	}
	if endResp == nil || len(endResp.Images) != 1 {
		t.Fatalf("OnImageEnd resp = %+v, want resp with 1 image", endResp)
	}
}

func TestGenerateImageLifecycleCallbacksError(t *testing.T) {
	m := &aitest.MockImageModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	var startCalls, endCalls int
	var endResp *provider.ImageResponse
	var endErr error
	_, err := GenerateImage(t.Context(), GenerateImageOpts{
		Model:  m,
		Prompt: "a cat",
		OnImageStart: func(call provider.ImageCall) {
			startCalls++
		},
		OnImageEnd: func(resp *provider.ImageResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 {
		t.Fatalf("OnImageStart calls = %d, want 1", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnImageEnd calls = %d, want 1", endCalls)
	}
	if endResp != nil {
		t.Fatalf("OnImageEnd resp = %+v, want nil", endResp)
	}
	if !errors.Is(endErr, err) {
		t.Fatalf("OnImageEnd err = %v, want same as returned err %v", endErr, err)
	}
}

func TestGenerateImageEmptyImages(t *testing.T) {
	m := &aitest.MockImageModel{Response: &provider.ImageResponse{Images: []provider.GeneratedImage{}}}
	_, err := GenerateImage(t.Context(), GenerateImageOpts{Model: m, Prompt: "a cat"})
	if err == nil {
		t.Fatal("want error when model returns no images")
	}
}
