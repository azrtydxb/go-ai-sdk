package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestOmitEmptyAuthAndStaticHeaders(t *testing.T) {
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		w.Header().Set("Content-Type", "audio/ogg")
		_, _ = io.WriteString(w, "OGG")
	}))
	defer srv.Close()

	cfg := Config{Name: "sh", BaseURL: srv.URL, OmitEmptyAuth: true, Headers: map[string]string{"X-A": "1", "authorization": "x"}}
	res, err := NewSpeechModel(cfg, "m").GenerateSpeech(context.Background(), provider.SpeechCall{Text: "t", Voice: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hdr["Authorization"]; ok {
		t.Errorf("Authorization present: %v", hdr["Authorization"])
	}
	if hdr.Get("X-A") != "1" {
		t.Errorf("X-A = %q", hdr.Get("X-A"))
	}
	if res.MediaType != "audio/ogg" {
		t.Errorf("MediaType = %q, want server Content-Type", res.MediaType)
	}

	// Without OmitEmptyAuth the legacy behavior is unchanged.
	cfg.OmitEmptyAuth = false
	if _, err := NewSpeechModel(cfg, "m").GenerateSpeech(context.Background(), provider.SpeechCall{Text: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := hdr["Authorization"]; !ok {
		t.Error("legacy: Authorization header expected")
	}
}

func TestTranscriptionFormatText(t *testing.T) {
	var format string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		format = r.FormValue("response_format")
		_, _ = io.WriteString(w, "hello world\n")
	}))
	defer srv.Close()
	m := NewTranscriptionModel(Config{Name: "sh", BaseURL: srv.URL, TranscriptionFormat: "text"}, "whisper")
	res, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x"), MediaType: "audio/wav", Language: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if format != "text" || res.Text != "hello world" {
		t.Errorf("format=%q text=%q", format, res.Text)
	}
}
