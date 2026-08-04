package openaicompat

// Request-shape tests for NewTranslationModel, white-box (package
// openaicompat). Mirrors transcription_test.go: the wire body is
// multipart/form-data, so tests re-parse the recorded raw request body
// with mime/multipart, using the recorded Content-Type header to recover
// the boundary. Uses its own local httptest fixture (rather than
// compattest, which doesn't speak /audio/translations) since this is the
// only package exercising that endpoint.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// translationFixtureServer records the last request (raw body + headers)
// and responds with the canned verbose_json translation response.
type translationFixtureServer struct {
	*httptest.Server
	lastBody    []byte
	lastHeaders http.Header
}

func newTranslationFixtureServer(t *testing.T) *translationFixtureServer {
	t.Helper()
	s := &translationFixtureServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := readBody(t, w, r)
		if !ok {
			return
		}
		s.lastBody = body
		s.lastHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"hello world","language":"french","duration":1.5}`))
	}))
	t.Cleanup(s.Server.Close)
	return s
}

func readBody(t *testing.T, w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	t.Helper()
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return nil, false
	}
	return buf, true
}

func TestTranslationRequestShapeWhisperVerboseJSON(t *testing.T) {
	srv := newTranslationFixtureServer(t)
	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	resp, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
		Prompt:    "a hint",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}

	ct := srv.lastHeaders.Get("Content-Type")
	filename, fileCT, fileData, fields := parseMultipart(t, srv.lastBody, ct)

	if filename != "audio.mp3" {
		t.Errorf("filename = %q, want audio.mp3", filename)
	}
	if fileCT != "audio/mpeg" {
		t.Errorf("file Content-Type = %q, want audio/mpeg", fileCT)
	}
	if string(fileData) != "raw-audio-bytes" {
		t.Errorf("file data = %q, want raw-audio-bytes", fileData)
	}
	if fields["model"] != "whisper-1" {
		t.Errorf("model field = %q, want whisper-1", fields["model"])
	}
	if fields["prompt"] != "a hint" {
		t.Errorf("prompt field = %q, want 'a hint'", fields["prompt"])
	}
	if fields["response_format"] != "verbose_json" {
		t.Errorf("response_format field = %q, want verbose_json", fields["response_format"])
	}

	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", resp.Text)
	}
	if resp.Language != "french" {
		t.Errorf("Language = %q, want french", resp.Language)
	}
	if resp.DurationSec != 1.5 {
		t.Errorf("DurationSec = %v, want 1.5", resp.DurationSec)
	}
	if len(resp.Raw) == 0 {
		t.Error("Raw should be populated")
	}
}

func TestTranslationRequestShapeNoPrompt(t *testing.T) {
	srv := newTranslationFixtureServer(t)
	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	_, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}

	ct := srv.lastHeaders.Get("Content-Type")
	filename, _, _, fields := parseMultipart(t, srv.lastBody, ct)
	if filename != "audio.wav" {
		t.Errorf("filename = %q, want audio.wav", filename)
	}
	if _, ok := fields["prompt"]; ok {
		t.Errorf("prompt field unexpectedly present: %v", fields)
	}
}

func TestTranslationProviderOptionsMerge(t *testing.T) {
	srv := newTranslationFixtureServer(t)
	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	_, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg",
		ProviderOptions: map[string]any{
			"test":           map[string]any{"temperature": 0.2},
			"other-provider": map[string]any{"ignored": true},
		},
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}

	ct := srv.lastHeaders.Get("Content-Type")
	_, _, _, fields := parseMultipart(t, srv.lastBody, ct)
	if fields["temperature"] != "0.2" {
		t.Errorf("temperature field = %q, want 0.2", fields["temperature"])
	}
	if _, ok := fields["ignored"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestTranslationEmptyBaseURLErrors(t *testing.T) {
	model := NewTranslationModel(Config{Name: "test", APIKey: "k"}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err == nil {
		t.Fatal("Translate: want error for empty BaseURL, got nil")
	}
}

func TestTranslation401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	model := NewTranslationModel(Config{Name: "test", APIKey: "bad-key", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if apiErr.Message != "invalid api key" {
		t.Errorf("Message = %q, want parsed message %q", apiErr.Message, "invalid api key")
	}
}

func TestTranslation429Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want 429", apiErr.StatusCode)
	}
}

func TestTranslationCtxCancel(t *testing.T) {
	srv := newTranslationFixtureServer(t)
	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := model.Translate(ctx, provider.TranslationCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// --- Security: multipart CRLF/quote injection guard ---
//
// mime/multipart.Writer writes MIME headers verbatim with no CRLF
// validation (unlike net/http), so a caller-supplied MediaType or
// ProviderOptions key/value containing "\r\n" could otherwise forge extra
// multipart headers or parts. These tests confirm such values are
// rejected before anything is sent to the server.

func TestTranslationMediaTypeCRLFRejected(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg\r\nX-Injected: 1",
	})
	if err == nil {
		t.Fatal("Translate: want error for MediaType containing CRLF")
	}
	if hit {
		t.Error("Translate: request was sent despite invalid MediaType")
	}
}

func TestTranslationProviderOptionKeyNewlineRejected(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	model := NewTranslationModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")
	_, err := model.Translate(context.Background(), provider.TranslationCall{
		Audio:     []byte("x"),
		MediaType: "audio/mpeg",
		ProviderOptions: map[string]any{
			"test": map[string]any{
				"evil\nX-Injected: 1": "v",
			},
		},
	})
	if err == nil {
		t.Fatal("Translate: want error for ProviderOptions key containing LF")
	}
	if hit {
		t.Error("Translate: request was sent despite invalid provider option key")
	}
}
