package elevenlabs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranscribe_HappyPath(t *testing.T) {
	var gotHeader, gotContentType string
	var gotModelID, gotLanguageCode, gotFileContentType string
	var gotFileBytes []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("xi-api-key")
		gotContentType = r.Header.Get("Content-Type")

		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		gotModelID = r.FormValue("model_id")
		gotLanguageCode = r.FormValue("language_code")

		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer file.Close()
		gotFileContentType = header.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := file.Read(buf)
		gotFileBytes = buf[:n]

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"text": "hello world",
			"language_code": "en",
			"words": [
				{"text":"hello","start":0.0,"end":0.5,"type":"word"},
				{"text":" ","start":0.5,"end":0.6,"type":"spacing"},
				{"text":"world","start":0.6,"end":1.2,"type":"word"}
			]
		}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("fake-audio-bytes"),
		MediaType: "audio/mpeg",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if gotHeader != "test-key" {
		t.Errorf("xi-api-key header = %q", gotHeader)
	}
	if gotContentType == "" {
		t.Error("Content-Type header should be set (multipart)")
	}
	if gotModelID != "scribe_v1" {
		t.Errorf("model_id = %q", gotModelID)
	}
	if gotLanguageCode != "en" {
		t.Errorf("language_code = %q", gotLanguageCode)
	}
	if gotFileContentType != "audio/mpeg" {
		t.Errorf("file Content-Type = %q", gotFileContentType)
	}
	if string(gotFileBytes) != "fake-audio-bytes" {
		t.Errorf("file bytes = %q", gotFileBytes)
	}

	if resp.Text != "hello world" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Language != "en" {
		t.Errorf("Language = %q", resp.Language)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2 (word-type only)", len(resp.Segments))
	}
	if resp.Segments[0].Text != "hello" || resp.Segments[0].StartSec != 0.0 || resp.Segments[0].EndSec != 0.5 {
		t.Errorf("Segments[0] = %+v", resp.Segments[0])
	}
	if resp.Segments[1].Text != "world" || resp.Segments[1].StartSec != 0.6 || resp.Segments[1].EndSec != 1.2 {
		t.Errorf("Segments[1] = %+v", resp.Segments[1])
	}
	if resp.DurationSec != 1.2 {
		t.Errorf("DurationSec = %v, want 1.2 (last word's end)", resp.DurationSec)
	}
}

func TestTranscribe_NoLanguageOmitsField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		if _, ok := r.MultipartForm.Value["language_code"]; ok {
			t.Error("language_code should not be present when Language is empty")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"hi","words":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
}

func TestTranscribe_DurationIgnoresTrailingNonWordEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"text": "hello world",
			"language_code": "en",
			"words": [
				{"text":"hello","start":0.0,"end":0.5,"type":"word"},
				{"text":"world","start":0.6,"end":1.2,"type":"word"},
				{"text":" ","start":1.2,"end":2.5,"type":"spacing"}
			]
		}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("fake-audio-bytes"),
		MediaType: "audio/mpeg",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if len(resp.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(resp.Segments))
	}
	if resp.DurationSec != 1.2 {
		t.Errorf("DurationSec = %v, want 1.2 (last WORD's end, not trailing spacing's end 2.5)", resp.DurationSec)
	}
}

func TestTranscribe_EmptyWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"hello world","language_code":"en","words":[]}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("fake-audio-bytes"),
		MediaType: "audio/mpeg",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if len(resp.Segments) != 0 {
		t.Errorf("Segments len = %d, want 0", len(resp.Segments))
	}
	if resp.DurationSec != 0 {
		t.Errorf("DurationSec = %v, want 0", resp.DurationSec)
	}
	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want %q", resp.Text, "hello world")
	}
}

func TestTranscribe_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"detail":"missing api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey(""), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg",
	})
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
	if apiErr.Message != "missing api key" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestTranscribe_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("scribe_v1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Transcribe(ctx, provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg",
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
