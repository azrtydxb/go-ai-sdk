package openaicompat

// Wire-level tests: provider.Call-family Headers must reach the outgoing
// request, and a header whose name case-insensitively matches the
// provider's auth header must NOT be able to override the real
// credential — same contract as internal/httpheader.Apply and the
// language path (openaicompat.go:applyExtraHeaders).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestEmbedding_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1]}]}`))
	}))
	t.Cleanup(srv.Close)

	model := NewEmbeddingModel(Config{Name: "test", APIKey: "real-key", BaseURL: srv.URL}, "test-embed")
	em, ok := model.(provider.EmbeddingModelWithOptions)
	if !ok {
		t.Fatal("embeddingModel does not implement EmbeddingModelWithOptions")
	}

	_, err := em.EmbedCall(context.Background(), provider.EmbeddingCall{
		Values:  []string{"a"},
		Headers: map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("EmbedCall: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-key" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-key")
	}
}

func TestImage_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	model := NewImageModel(Config{Name: "test", APIKey: "real-key", BaseURL: srv.URL}, "test-image")
	_, err := model.GenerateImages(context.Background(), provider.ImageCall{
		Prompt:  "a cat",
		Headers: map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("GenerateImages: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-key" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-key")
	}
}

func TestSpeech_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte("audio-bytes"))
	}))
	t.Cleanup(srv.Close)

	model := NewSpeechModel(Config{Name: "test", APIKey: "real-key", BaseURL: srv.URL}, "test-speech")
	_, err := model.GenerateSpeech(context.Background(), provider.SpeechCall{
		Text:    "hello",
		Headers: map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-key" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-key")
	}
}

func TestTranscription_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"hi"}`))
	}))
	t.Cleanup(srv.Close)

	model := NewTranscriptionModel(Config{Name: "test", APIKey: "real-key", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("fake-audio"),
		MediaType: "audio/wav",
		Headers:   map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-key" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-key")
	}
	if gotContentType == "" || gotContentType == "yes" {
		t.Errorf("Content-Type header = %q, want multipart content type to survive (not clobbered by unrelated Headers keys)", gotContentType)
	}
}

func TestTranslation_HeadersApplied(t *testing.T) {
	var gotExtra, gotAuth, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtra = r.Header.Get("X-Test")
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"hi"}`))
	}))
	t.Cleanup(srv.Close)

	model := NewTranslationModel(Config{Name: "test", APIKey: "real-key", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("fake-audio"),
		MediaType: "audio/wav",
		Headers:   map[string]string{"X-Test": "yes", "Authorization": "attacker-supplied"},
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if gotExtra != "yes" {
		t.Errorf("X-Test header = %q, want %q", gotExtra, "yes")
	}
	if gotAuth != "Bearer real-key" {
		t.Errorf("Authorization header = %q, want %q (must not be overridden by Headers)", gotAuth, "Bearer real-key")
	}
	if gotContentType == "" || gotContentType == "yes" {
		t.Errorf("Content-Type header = %q, want multipart content type to survive (not clobbered by unrelated Headers keys)", gotContentType)
	}
}
