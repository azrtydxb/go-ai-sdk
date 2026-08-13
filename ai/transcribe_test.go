package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranscribeHappyPath(t *testing.T) {
	m := &aitest.MockTranscriptionModel{Response: &provider.TranscriptionResponse{
		Text: "hello world",
		Segments: []provider.TranscriptSegment{
			{Text: "hello", StartSec: 0, EndSec: 0.5},
			{Text: "world", StartSec: 0.5, EndSec: 1.0},
		},
		Language:    "en",
		DurationSec: 1.0,
	}}
	res, err := Transcribe(t.Context(), TranscribeOpts{
		Model:     m,
		Audio:     []byte("audio-bytes"),
		MediaType: "audio/mpeg",
		Language:  "en",
		Prompt:    "context",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if string(call.Audio) != "audio-bytes" || call.MediaType != "audio/mpeg" || call.Language != "en" || call.Prompt != "context" {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if res.Text != "hello world" || res.Language != "en" || res.DurationSec != 1.0 {
		t.Fatalf("result mapped incorrectly: %+v", res)
	}
	if len(res.Segments) != 2 {
		t.Fatalf("Segments = %d, want 2", len(res.Segments))
	}
}

func TestTranscribeNilModel(t *testing.T) {
	_, err := Transcribe(t.Context(), TranscribeOpts{Audio: []byte("x")})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestTranscribeEmptyAudio(t *testing.T) {
	m := &aitest.MockTranscriptionModel{}
	_, err := Transcribe(t.Context(), TranscribeOpts{Model: m})
	if !errors.Is(err, ErrAudioRequired) {
		t.Fatalf("err = %v, want ErrAudioRequired", err)
	}
}

func TestTranscribeRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockTranscriptionModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	_, err := Transcribe(t.Context(), TranscribeOpts{Model: m, Audio: []byte("x")})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.Calls))
	}
}

func TestTranscribeLifecycleCallbacksSuccess(t *testing.T) {
	m := &aitest.MockTranscriptionModel{Response: &provider.TranscriptionResponse{Text: "hello"}}
	var startCalls int
	var startCall provider.TranscriptionCall
	var endCalls int
	var endResp *provider.TranscriptionResponse
	var endErr error
	_, err := Transcribe(t.Context(), TranscribeOpts{
		Model: m,
		Audio: []byte("x"),
		OnTranscribeStart: func(call provider.TranscriptionCall) {
			startCalls++
			startCall = call
		},
		OnTranscribeEnd: func(resp *provider.TranscriptionResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 {
		t.Fatalf("OnTranscribeStart calls = %d, want 1", startCalls)
	}
	if string(startCall.Audio) != "x" {
		t.Fatalf("OnTranscribeStart call = %+v, want built call", startCall)
	}
	if endCalls != 1 {
		t.Fatalf("OnTranscribeEnd calls = %d, want 1", endCalls)
	}
	if endErr != nil {
		t.Fatalf("OnTranscribeEnd err = %v, want nil", endErr)
	}
	if endResp == nil || endResp.Text != "hello" {
		t.Fatalf("OnTranscribeEnd resp = %+v, want resp with Text=hello", endResp)
	}
}

func TestTranscribeLifecycleCallbacksError(t *testing.T) {
	m := &aitest.MockTranscriptionModel{Err: NewAPICallError(500, "https://x", "", "boom")}
	var startCalls, endCalls int
	var endResp *provider.TranscriptionResponse
	var endErr error
	_, err := Transcribe(t.Context(), TranscribeOpts{
		Model: m,
		Audio: []byte("x"),
		OnTranscribeStart: func(call provider.TranscriptionCall) {
			startCalls++
		},
		OnTranscribeEnd: func(resp *provider.TranscriptionResponse, err error) {
			endCalls++
			endResp = resp
			endErr = err
		},
	})
	if err == nil {
		t.Fatal("want error")
	}
	if startCalls != 1 {
		t.Fatalf("OnTranscribeStart calls = %d, want 1", startCalls)
	}
	if endCalls != 1 {
		t.Fatalf("OnTranscribeEnd calls = %d, want 1", endCalls)
	}
	if endResp != nil {
		t.Fatalf("OnTranscribeEnd resp = %+v, want nil", endResp)
	}
	if !errors.Is(endErr, err) {
		t.Fatalf("OnTranscribeEnd err = %v, want same as returned err %v", endErr, err)
	}
}

func TestTranscribeEmptyText(t *testing.T) {
	// An empty transcript is a legitimate successful result (e.g. silent or
	// non-speech audio) — it must not be treated as an error.
	m := &aitest.MockTranscriptionModel{Response: &provider.TranscriptionResponse{Text: ""}}
	res, err := Transcribe(t.Context(), TranscribeOpts{Model: m, Audio: []byte("x")})
	if err != nil {
		t.Fatalf("err = %v, want nil for empty transcript text", err)
	}
	if res.Text != "" {
		t.Fatalf("Text = %q, want empty", res.Text)
	}
}
