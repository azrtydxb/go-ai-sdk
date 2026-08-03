package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranslateHappyPath(t *testing.T) {
	m := &aitest.MockTranslationModel{Response: &provider.TranslationResponse{
		Text:        "hello world",
		Language:    "french",
		DurationSec: 1.0,
	}}
	res, err := Translate(t.Context(), TranslateOpts{
		Model:     m,
		Audio:     []byte("audio-bytes"),
		MediaType: "audio/mpeg",
		Prompt:    "context",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if string(call.Audio) != "audio-bytes" || call.MediaType != "audio/mpeg" || call.Prompt != "context" {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if res.Text != "hello world" || res.Language != "french" || res.DurationSec != 1.0 {
		t.Fatalf("result mapped incorrectly: %+v", res)
	}
}

func TestTranslateNilModel(t *testing.T) {
	_, err := Translate(t.Context(), TranslateOpts{Audio: []byte("x")})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestTranslateEmptyAudio(t *testing.T) {
	m := &aitest.MockTranslationModel{}
	_, err := Translate(t.Context(), TranslateOpts{Model: m})
	if !errors.Is(err, ErrAudioRequired) {
		t.Fatalf("err = %v, want ErrAudioRequired", err)
	}
}

func TestTranslateRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockTranslationModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := Translate(t.Context(), TranslateOpts{Model: m, Audio: []byte("x")})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}

func TestTranslateEmptyText(t *testing.T) {
	// An empty translation is a legitimate successful result (e.g. silent
	// audio) — it must not be treated as an error.
	m := &aitest.MockTranslationModel{Response: &provider.TranslationResponse{Text: ""}}
	res, err := Translate(t.Context(), TranslateOpts{Model: m, Audio: []byte("x")})
	if err != nil {
		t.Fatalf("err = %v, want nil for empty translation text", err)
	}
	if res.Text != "" {
		t.Fatalf("Text = %q, want empty", res.Text)
	}
}
