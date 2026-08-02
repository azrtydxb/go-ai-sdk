package elevenlabs

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
	var gotPath, gotQuery, gotHeader string
	var gotBody speechRequest
	audio := []byte("fake-mp3-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Get("xi-api-key")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:     "hello world",
		Voice:    "myvoice123",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/v1/text-to-speech/myvoice123" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "output_format=mp3_44100_128" {
		t.Errorf("query = %q", gotQuery)
	}
	if gotHeader != "test-key" {
		t.Errorf("xi-api-key header = %q", gotHeader)
	}
	if gotBody.Text != "hello world" {
		t.Errorf("body.Text = %q", gotBody.Text)
	}
	if gotBody.ModelID != "eleven_multilingual_v2" {
		t.Errorf("body.ModelID = %q", gotBody.ModelID)
	}
	if gotBody.LanguageCode != "en" {
		t.Errorf("body.LanguageCode = %q", gotBody.LanguageCode)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio = %q", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q", resp.MediaType)
	}
}

func TestGenerateSpeech_DefaultVoiceAndNoLanguage(t *testing.T) {
	var gotPath string
	var raw map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/v1/text-to-speech/"+defaultVoiceID {
		t.Errorf("path = %q, want default voice", gotPath)
	}
	if _, ok := raw["language_code"]; ok {
		t.Errorf("language_code should be omitted, got %v", raw["language_code"])
	}
}

func TestOutputFormatWire(t *testing.T) {
	tests := []struct {
		in, wantFmt, wantMedia string
	}{
		{"", "mp3_44100_128", "audio/mpeg"},
		{"mp3", "mp3_44100_128", "audio/mpeg"},
		{"pcm", "pcm_44100", "audio/pcm"},
		{"ulaw", "ulaw_8000", "audio/basic"},
		{"opus_48000_64", "opus_48000_64", "application/octet-stream"},
	}
	for _, tt := range tests {
		gotFmt, gotMedia := outputFormatWire(tt.in)
		if gotFmt != tt.wantFmt || gotMedia != tt.wantMedia {
			t.Errorf("outputFormatWire(%q) = (%q, %q), want (%q, %q)", tt.in, gotFmt, gotMedia, tt.wantFmt, tt.wantMedia)
		}
	}
}

func TestGenerateSpeech_FormatMappingInQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", OutputFormat: "pcm"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if gotQuery != "output_format=pcm_44100" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestGenerateSpeech_Speed(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	speed := 1.25
	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Speed: &speed})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	voiceSettings, ok := raw["voice_settings"].(map[string]any)
	if !ok {
		t.Fatalf("voice_settings missing or wrong type, got %v", raw["voice_settings"])
	}
	if voiceSettings["speed"] != 1.25 {
		t.Errorf("voice_settings.speed = %v, want 1.25", voiceSettings["speed"])
	}
}

func TestGenerateSpeech_NoSpeedOmitsVoiceSettings(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if _, ok := raw["voice_settings"]; ok {
		t.Errorf("voice_settings should be omitted when Speed is nil, got %v", raw["voice_settings"])
	}
}

func TestGenerateSpeech_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("eleven_multilingual_v2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !asAPICallError(err, &apiErr) {
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
	m := p.SpeechModel("eleven_multilingual_v2")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateSpeech(ctx, provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// asAPICallError is a small helper to keep error-assertion terse in tests.
func asAPICallError(err error, target **ai.APICallError) bool {
	ae, ok := err.(*ai.APICallError)
	if ok {
		*target = ae
	}
	return ok
}
