package cartesia

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
	var gotPath, gotAuth, gotVersion string
	var gotBody speechRequest
	audio := []byte("fake-mp3-bytes")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Cartesia-Version")
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
	m := p.SpeechModel("sonic-2")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:     "hello world",
		Voice:    "voice-123",
		Language: "en",
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/tts/bytes" {
		t.Errorf("path = %q, want /tts/bytes", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotVersion != "2024-11-13" {
		t.Errorf("Cartesia-Version = %q, want 2024-11-13", gotVersion)
	}
	if gotBody.ModelID != "sonic-2" {
		t.Errorf("model_id = %q", gotBody.ModelID)
	}
	if gotBody.Transcript != "hello world" {
		t.Errorf("transcript = %q", gotBody.Transcript)
	}
	if gotBody.Voice.Mode != "id" || gotBody.Voice.ID != "voice-123" {
		t.Errorf("voice = %+v", gotBody.Voice)
	}
	if gotBody.OutputFormat.Container != "mp3" {
		t.Errorf("output_format.container = %q, want mp3", gotBody.OutputFormat.Container)
	}
	if gotBody.OutputFormat.Encoding != "mp3" {
		t.Errorf("output_format.encoding = %q, want mp3", gotBody.OutputFormat.Encoding)
	}
	if gotBody.OutputFormat.SampleRate != 44100 {
		t.Errorf("output_format.sample_rate = %d, want 44100", gotBody.OutputFormat.SampleRate)
	}
	if gotBody.Language != "en" {
		t.Errorf("language = %q", gotBody.Language)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio = %q", resp.Audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q", resp.MediaType)
	}
}

func TestGenerateSpeech_VoiceRequired(t *testing.T) {
	p := New(WithAPIKey("k"))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error when Voice is empty")
	}
}

func TestGenerateSpeech_LanguageOmittedWhenEmpty(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if _, ok := raw["language"]; ok {
		t.Errorf("language should be omitted, got %v", raw["language"])
	}
}

func TestOutputFormatMapping(t *testing.T) {
	tests := []struct {
		container, wantEncoding, wantMediaType string
	}{
		{"", "mp3", "audio/mpeg"},
		{"mp3", "mp3", "audio/mpeg"},
		{"wav", "pcm_s16le", "audio/wav"},
		{"raw", "pcm_f32le", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := encodingForContainer(tt.container); got != tt.wantEncoding {
			t.Errorf("encodingForContainer(%q) = %q, want %q", tt.container, got, tt.wantEncoding)
		}
		if got := mediaTypeForContainer(tt.container); got != tt.wantMediaType {
			t.Errorf("mediaTypeForContainer(%q) = %q, want %q", tt.container, got, tt.wantMediaType)
		}
	}
}

func TestGenerateSpeech_OutputFormatWav(t *testing.T) {
	audio := []byte{0x52, 0x49, 0x46, 0x46}
	var gotBody speechRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
		w.Write(audio)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1", OutputFormat: "wav"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if gotBody.OutputFormat.Container != "wav" {
		t.Errorf("container = %q, want wav", gotBody.OutputFormat.Container)
	}
	if resp.MediaType != "audio/wav" {
		t.Errorf("MediaType = %q, want audio/wav", resp.MediaType)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio mismatch")
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
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hi",
		Voice: "v1",
		ProviderOptions: map[string]any{
			"cartesia": map[string]any{"speed": "slow"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if raw["speed"] != "slow" {
		t.Errorf("speed = %v, want slow", raw["speed"])
	}
}

func TestGenerateSpeech_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("sonic-2")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Voice: "v1"})
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
	m := p.SpeechModel("sonic-2")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateSpeech(ctx, provider.SpeechCall{Text: "hi", Voice: "v1"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
