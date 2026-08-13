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

func TestTranslateLifecycleCallbacksSuccess(t *testing.T) {
	m := &aitest.MockTranslationModel{Response: &provider.TranslationResponse{Text: "hello"}}
	var startCalls int
	var startCall provider.TranslationCall
	var endCalls int
	var endResp *provider.TranslationResponse
	var endErr error
	_, err := Translate(t.Context(), TranslateOpts{
		Model: m,
		Audio: []byte("x"),
		OnTranslateStart: func(call provider.TranslationCall) {
			startCalls++
			startCall = call
		},
		OnTranslateEnd: func(resp *provider.TranslationResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 {
		t.Fatalf("OnTranslateStart calls = %d, want 1", startCalls)
	}
	if string(startCall.Audio) != "x" {
		t.Fatalf("OnTranslateStart call = %+v, want built call", startCall)
	}
	if endCalls != 1 {
		t.Fatalf("OnTranslateEnd calls = %d, want 1", endCalls)
	}
	if endErr != nil {
		t.Fatalf("OnTranslateEnd err = %v, want nil", endErr)
	}
	if endResp == nil || endResp.Text != "hello" {
		t.Fatalf("OnTranslateEnd resp = %+v, want resp with Text=hello", endResp)
	}
}

func TestTranslateLifecycleCallbacksError(t *testing.T) {
	m := &aitest.MockTranslationModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	var startCalls, endCalls int
	var endResp *provider.TranslationResponse
	var endErr error
	_, err := Translate(t.Context(), TranslateOpts{
		Model: m,
		Audio: []byte("x"),
		OnTranslateStart: func(call provider.TranslationCall) {
			startCalls++
		},
		OnTranslateEnd: func(resp *provider.TranslationResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 {
		t.Fatalf("OnTranslateStart calls = %d, want 1", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnTranslateEnd calls = %d, want 1", endCalls)
	}
	if endResp != nil {
		t.Fatalf("OnTranslateEnd resp = %+v, want nil", endResp)
	}
	if !errors.Is(endErr, err) {
		t.Fatalf("OnTranslateEnd err = %v, want same as returned err %v", endErr, err)
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
