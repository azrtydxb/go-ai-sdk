package hume

import (
	"context"
	"encoding/base64"
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
	encoded := base64.StdEncoding.EncodeToString(audio)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeader = r.Header.Get("X-Hume-Api-Key")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{
			Generations: []generationWire{{Audio: encoded}},
		})
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	resp, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hello world",
		Voice: "myvoice123",
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if gotPath != "/v0/tts" {
		t.Errorf("path = %q", gotPath)
	}
	if gotHeader != "test-key" {
		t.Errorf("X-Hume-Api-Key header = %q", gotHeader)
	}
	if len(gotBody.Utterances) != 1 {
		t.Fatalf("Utterances len = %d", len(gotBody.Utterances))
	}
	if gotBody.Utterances[0].Text != "hello world" {
		t.Errorf("Utterances[0].Text = %q", gotBody.Utterances[0].Text)
	}
	if gotBody.Utterances[0].Voice == nil || gotBody.Utterances[0].Voice.Name != "myvoice123" {
		t.Errorf("Utterances[0].Voice = %+v", gotBody.Utterances[0].Voice)
	}
	if gotBody.Format.Type != "mp3" {
		t.Errorf("Format.Type = %q", gotBody.Format.Type)
	}
	if string(resp.Audio) != string(audio) {
		t.Errorf("Audio = %q, want %q", resp.Audio, audio)
	}
	if resp.MediaType != "audio/mpeg" {
		t.Errorf("MediaType = %q", resp.MediaType)
	}
}

func TestGenerateSpeech_DefaultsNoVoice(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	utterances, ok := raw["utterances"].([]any)
	if !ok || len(utterances) != 1 {
		t.Fatalf("utterances = %v", raw["utterances"])
	}
	utt := utterances[0].(map[string]any)
	if _, ok := utt["voice"]; ok {
		t.Errorf("voice should be omitted, got %v", utt["voice"])
	}
	if _, ok := utt["speed"]; ok {
		t.Errorf("speed should be omitted, got %v", utt["speed"])
	}
	format, ok := raw["format"].(map[string]any)
	if !ok || format["type"] != "mp3" {
		t.Errorf("format = %v, want type mp3", raw["format"])
	}
}

func TestGenerateSpeech_Speed(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	speed := 0.8
	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Speed: &speed})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	utterances := raw["utterances"].([]any)
	utt := utterances[0].(map[string]any)
	if utt["speed"] != 0.8 {
		t.Errorf("speed = %v, want 0.8", utt["speed"])
	}
}

func TestGenerateSpeech_LanguageIgnored(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi", Language: "en"})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if _, ok := raw["language"]; ok {
		t.Errorf("language should not appear in wire body, got %v", raw["language"])
	}
}

func TestGenerateSpeech_ProviderOptionsMerge(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text: "hi",
		ProviderOptions: map[string]any{
			"hume": map[string]any{"num_generations": float64(2)},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if raw["num_generations"] != float64(2) {
		t.Errorf("num_generations = %v, want 2", raw["num_generations"])
	}
}

func TestGenerateSpeech_VoiceProviderOption(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:  "hi",
		Voice: "some-library-voice",
		ProviderOptions: map[string]any{
			"hume": map[string]any{"voice_provider": "HUME_AI"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	utterances, ok := raw["utterances"].([]any)
	if !ok || len(utterances) != 1 {
		t.Fatalf("utterances = %v", raw["utterances"])
	}
	utt := utterances[0].(map[string]any)
	voice, ok := utt["voice"].(map[string]any)
	if !ok {
		t.Fatalf("voice = %v, want object", utt["voice"])
	}
	if voice["name"] != "some-library-voice" {
		t.Errorf("voice.name = %v", voice["name"])
	}
	if voice["provider"] != "HUME_AI" {
		t.Errorf("voice.provider = %v, want HUME_AI", voice["provider"])
	}
	// voice_provider must not leak into the wire body as a top-level key.
	if _, ok := raw["voice_provider"]; ok {
		t.Errorf("voice_provider should not appear top-level, got %v", raw["voice_provider"])
	}
}

func TestGenerateSpeech_VoiceProviderOptionIgnoredWithoutVoice(t *testing.T) {
	var raw map[string]any
	encoded := base64.StdEncoding.EncodeToString([]byte("x"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &raw)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: encoded}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text: "hi",
		ProviderOptions: map[string]any{
			"hume": map[string]any{"voice_provider": "HUME_AI"},
		},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	utterances := raw["utterances"].([]any)
	utt := utterances[0].(map[string]any)
	if _, ok := utt["voice"]; ok {
		t.Errorf("voice should be omitted when call.Voice is empty, got %v", utt["voice"])
	}
}

func TestMediaTypeForFormat(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "audio/mpeg"},
		{"mp3", "audio/mpeg"},
		{"wav", "audio/wav"},
		{"pcm", "audio/pcm"},
		{"ogg", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := mediaTypeForFormat(tt.in); got != tt.want {
			t.Errorf("mediaTypeForFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGenerateSpeech_EmptyGenerationsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: nil})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error for empty generations")
	}
}

func TestGenerateSpeech_EmptyAudioError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(speechResponseWire{Generations: []generationWire{{Audio: ""}}})
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error for empty audio")
	}
}

func TestGenerateSpeech_Unauthorized_MessageField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

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

func TestGenerateSpeech_Unauthorized_ErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.SpeechModel("some-model")

	_, err := m.GenerateSpeech(context.Background(), provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*ai.APICallError)
	if !ok {
		t.Fatalf("expected *ai.APICallError, got %T: %v", err, err)
	}
	if apiErr.Message != "bad key" {
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
	m := p.SpeechModel("some-model")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.GenerateSpeech(ctx, provider.SpeechCall{Text: "hi"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
