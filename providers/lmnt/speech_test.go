package lmnt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestGenerateSpeech_HappyPath(t *testing.T) {
	var gotPath, gotHeader string
	var gotBody speechRequest
	audio := []byte("fake-mp3-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-API-Key")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:     "hello world",
		Voice:    "myvoice123",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/v1/ai/speech/bytes" {
		t.Errorf("path = %q", gotPath)
	}
	if gotHeader != "test-key" {
		t.Errorf("X-API-Key header = %q", gotHeader)
	}
	if gotBody.Text != "hello world" {
		t.Errorf("body.Text = %q", gotBody.Text)
	}
	if gotBody.Voice != "myvoice123" {
		t.Errorf("body.Voice = %q", gotBody.Voice)
	}
	if gotBody.Model != "blizzard" {
		t.Errorf("body.Model = %q", gotBody.Model)
	}
	if gotBody.Format != "mp3" {
		t.Errorf("body.Format = %q", gotBody.Format)
	}
	if gotBody.Language != "en" {
		t.Errorf("body.Language = %q", gotBody.Language)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio = %q", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q", resp.MediaType)
	}
}

func TestGenerateSpeech_Defaults(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if raw["voice"] != defaultVoice {
		t.Errorf("voice = %v, want %q", raw["voice"], defaultVoice)
	}
	if raw["format"] != "mp3" {
		t.Errorf("format = %v, want mp3", raw["format"])
	}
	if _, ok := raw["language"]; ok {
		t.Errorf("language should be omitted, got %v", raw["language"])
	}
	if _, ok := raw["speed"]; ok {
		t.Errorf("speed should be omitted, got %v", raw["speed"])
	}
}

func TestGenerateSpeech_Speed(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	speed := 1.5
	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Speed: &speed})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if raw["speed"] != 1.5 {
		t.Errorf("speed = %v, want 1.5", raw["speed"])
	}
}

func TestGenerateSpeech_ProviderOptionsMerge(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text: "hi",
		ProviderOptions: map[string]any{
			"lmnt": map[string]any{"seed": float64(42), "voice": "override-voice"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if raw["seed"] != float64(42) {
		t.Errorf("seed = %v, want 42", raw["seed"])
	}
	if raw["voice"] != "override-voice" {
		t.Errorf("voice = %v, want override-voice", raw["voice"])
	}
}

func TestMediaTypeForFormat(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "audio/mpeg"},
		{"mp3", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"aac", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := mediaTypeForFormat(tt.in); got != tt.want {
			t.Errorf("mediaTypeForFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGenerateSpeech_RawAudioBytes(t *testing.T) {
	audio := []byte{0x00, 0x01, 0xFF, 0xAB, 0xCD}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", OutputFormat: "wav"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio mismatch, got %v want %v", resp.Audio, audio)
	}
	if resp.MediaType != "audio/wav" {
		t.Errorf("MediaType = %q, want audio/wav", resp.MediaType)
	}
}

func TestGenerateSpeech_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*ai.APICallError)
	if !ok {
		t.Fatalf("expected *ai.APICallError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestGenerateSpeech_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("blizzard")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateSpeech(ctx, provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
