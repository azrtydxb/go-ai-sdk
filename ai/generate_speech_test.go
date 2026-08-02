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

func TestGenerateSpeechEmptyAudio(t *testing.T) {
	m := &aitest.MockSpeechModel{Response: &provider.SpeechResponse{Audio: []byte{}}}
	_, err := GenerateSpeech(t.Context(), GenerateSpeechOpts{Model: m, Text: "hi"})
	if err == nil {
		t.Fatal("want error when model returns no audio")
	}
}
