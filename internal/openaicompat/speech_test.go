package openaicompat

// Request-shape tests for NewSpeechModel, white-box (package openaicompat)
// since they exercise speechRequest directly.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestSpeechRequestShapeDefaults(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewSpeechModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "tts-1")

	resp, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hello"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	var req speechRequest
	if err := json.Unmarshal(reqs[0], &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.Model != "tts-1" || req.Input != "hello" {
		t.Errorf("request = %+v, want model tts-1, input hello", req)
	}
	if req.Voice != "alloy" {
		t.Errorf("voice = %q, want default alloy", req.Voice)
	}
	if req.ResponseFormat != "mp3" {
		t.Errorf("response_format = %q, want default mp3", req.ResponseFormat)
	}

	if string(resp.Audio) != "FAKEAUDIO" {
		t.Errorf("Audio = %q, want FAKEAUDIO", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q, want audio/mpeg", resp.MediaType)
	}
}

func TestSpeechRequestShapeExplicitVoiceAndFormat(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewSpeechModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "tts-1")

	speed := 1.5
	_, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:         "hello",
		Voice:        "nova",
		OutputFormat: "wav",
		Speed:        &speed,
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	reqs := srv.Requests()
	var req speechRequest
	json.Unmarshal(reqs[len(reqs)-1], &req)
	if req.Voice != "nova" {
		t.Errorf("voice = %q, want nova", req.Voice)
	}
	if req.ResponseFormat != "wav" {
		t.Errorf("response_format = %q, want wav", req.ResponseFormat)
	}
	if req.Speed == nil || *req.Speed != 1.5 {
		t.Errorf("speed = %v, want 1.5", req.Speed)
	}
}

func TestSpeechMediaTypeMapping(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{"mp3", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"opus", "audio/opus"},
		{"aac", "audio/aac"},
		{"flac", "audio/flac"},
		{"pcm", "audio/pcm"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			srv := compattest.NewFixtureServer(t, "test")
			model := NewSpeechModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "tts-1")
			resp, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{
				Text:         "hello",
				OutputFormat: tc.format,
			})
			if err != nil {
				t.Fatalf("GenerateSpeech: %v", err)
			}
			if resp.MediaType != tc.want {
				t.Errorf("format %s: MediaType = %q, want %q", tc.format, resp.MediaType, tc.want)
			}
		})
	}
}

func TestSpeechEmptyBaseURLErrors(t *testing.T) {
	model := NewSpeechModel(Config{Name: "test", APIKey: "k"}, "tts-1")
	_, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hello"})
	if err == nil {
		t.Fatal("GenerateSpeech: want error for empty BaseURL, got nil")
	}
}

func TestSpeechAuthHeader(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewSpeechModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL, APIKeyHeader: "api-key"}, "tts-1")

	_, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hello"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if got := srv.HeaderValues("api-key"); len(got) != 1 || got[0] != "k" {
		t.Errorf("api-key header = %v, want [k]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization header = %v, want [\"\"]", got)
	}
}
