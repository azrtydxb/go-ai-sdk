package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateSpeechHappyPath(t *testing.T) {
	m := &aitest.MockSpeechModel{Response: &provider.SpeechResponse{
		Audio:     []byte("audio-bytes"),
		MediaType: "audio/mpeg",
	}}
	speed := 1.5
	res, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{
		Model:        m,
		Text:         "hello world",
		Voice:        "alloy",
		OutputFormat: "mp3",
		Speed:        &speed,
		Language:     "en-US",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if call.Text != "hello world" || call.Voice != "alloy" || call.OutputFormat != "mp3" || call.Language != "en-US" {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if call.Speed == nil || *call.Speed != 1.5 {
		t.Fatalf("call.Speed = %v, want 1.5", call.Speed)
	}
	if string(res.Audio) != "audio-bytes" || res.MediaType != "audio/mpeg" {
		t.Fatalf("result mapped incorrectly: %+v", res)
	}
}

func TestGenerateSpeechNilModel(t *testing.T) {
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Text: "hi"})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestGenerateSpeechEmptyText(t *testing.T) {
	m := &aitest.MockSpeechModel{}
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Model: m})
	if !errors.Is(err, ErrTextRequired) {
		t.Fatalf("err = %v, want ErrTextRequired", err)
	}
}

func TestGenerateSpeechRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockSpeechModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Model: m, Text: "hi"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}

func TestGenerateSpeechLifecycleCallbacksSuccess(t *testing.T) {
	m := &aitest.MockSpeechModel{Response: &provider.SpeechResponse{Audio: []byte("a"), MediaType: "audio/mpeg"}}
	var startCalls int
	var startCall provider.SpeechCall
	var endCalls int
	var endResp *provider.SpeechResponse
	var endErr error
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{
		Model: m,
		Text:  "hi",
		OnSpeechStart: func(call provider.SpeechCall) {
			startCalls++
			startCall = call
		},
		OnSpeechEnd: func(resp *provider.SpeechResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 {
		t.Fatalf("OnSpeechStart calls = %d, want 1", startCalls)
	}
	if startCall.Text != "hi" {
		t.Fatalf("OnSpeechStart call = %+v, want built call", startCall)
	}
	if endCalls != 1 {
		t.Fatalf("OnSpeechEnd calls = %d, want 1", endCalls)
	}
	if endErr != nil {
		t.Fatalf("OnSpeechEnd err = %v, want nil", endErr)
	}
	if endResp == nil || string(endResp.Audio) != "a" {
		t.Fatalf("OnSpeechEnd resp = %+v, want resp with Audio=a", endResp)
	}
}

func TestGenerateSpeechLifecycleCallbacksError(t *testing.T) {
	m := &aitest.MockSpeechModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	var startCalls, endCalls int
	var endResp *provider.SpeechResponse
	var endErr error
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{
		Model: m,
		Text:  "hi",
		OnSpeechStart: func(call provider.SpeechCall) {
			startCalls++
		},
		OnSpeechEnd: func(resp *provider.SpeechResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 {
		t.Fatalf("OnSpeechStart calls = %d, want 1", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnSpeechEnd calls = %d, want 1", endCalls)
	}
	if endResp != nil {
		t.Fatalf("OnSpeechEnd resp = %+v, want nil", endResp)
	}
	if !errors.Is(endErr, err) {
		t.Fatalf("OnSpeechEnd err = %v, want same as returned err %v", endErr, err)
	}
}

func TestGenerateSpeechEmptyAudio(t *testing.T) {
	m := &aitest.MockSpeechModel{Response: &provider.SpeechResponse{Audio: []byte{}}}
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Model: m, Text: "hi"})
	if err == nil {
		t.Fatal("want error when model returns no audio")
	}
}
