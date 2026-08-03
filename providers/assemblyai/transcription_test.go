package assemblyai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// newFixtureServer wires up the three AssemblyAI endpoints used by the
// async transcription flow: upload, create, poll. pollBodies is served in
// order, one body per poll request; the last body is repeated if there are
// more poll requests than bodies.
func newFixtureServer(t *testing.T, pollBodies []string) (*httptest.Server, *int32, func() map[string]any, func() string, func() string) {
	t.Helper()

	var pollCount int32
	var gotUploadAuth, gotCreateAuth string
	var gotCreateBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		gotUploadAuth = r.Header.Get("Authorization")
		io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upload_url":"https://cdn.assemblyai.test/audio123"}`))
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		gotCreateAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotCreateBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"transcript-1","status":"queued"}`))
	})
	mux.HandleFunc("/v2/transcript/transcript-1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		idx := int(n) - 1
		if idx >= len(pollBodies) {
			idx = len(pollBodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(pollBodies[idx]))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, &pollCount,
		func() map[string]any { return gotCreateBody },
		func() string { return gotUploadAuth },
		func() string { return gotCreateAuth }
}

func TestTranscribe_RequestShapeAndMsToSecConversion(t *testing.T) {
	srv, pollCount, createBody, uploadAuth, createAuth := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"completed","text":"hello world","words":[{"text":"hello","start":0,"end":500},{"text":"world","start":500,"end":1200}],"language_code":"en","audio_duration":1.2}`,
	})

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if uploadAuth() != "test-key" {
		t.Errorf("upload Authorization = %q, want %q", uploadAuth(), "test-key")
	}
	if createAuth() != "test-key" {
		t.Errorf("create Authorization = %q, want %q", createAuth(), "test-key")
	}

	body := createBody()
	if body["audio_url"] != "https://cdn.assemblyai.test/audio123" {
		t.Errorf("audio_url = %v", body["audio_url"])
	}
	if body["speech_model"] != "universal" {
		t.Errorf("speech_model = %v", body["speech_model"])
	}
	if body["language_code"] != "en" {
		t.Errorf("language_code = %v", body["language_code"])
	}

	if resp.Text != "hello world" {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Language != "en" {
		t.Errorf("Language = %q", resp.Language)
	}
	if resp.DurationSec != 1.2 {
		t.Errorf("DurationSec = %v, want 1.2", resp.DurationSec)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments = %v", resp.Segments)
	}
	if resp.Segments[0].StartSec != 0 || resp.Segments[0].EndSec != 0.5 {
		t.Errorf("segment 0 = %+v, want start=0 end=0.5", resp.Segments[0])
	}
	if resp.Segments[1].StartSec != 0.5 || resp.Segments[1].EndSec != 1.2 {
		t.Errorf("segment 1 = %+v, want start=0.5 end=1.2", resp.Segments[1])
	}
	if atomic.LoadInt32(pollCount) != 1 {
		t.Errorf("pollCount = %d, want 1", *pollCount)
	}
}

func TestTranscribe_ModelIDOmittedWhenEmpty(t *testing.T) {
	srv, _, createBody, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"completed","text":"hi","audio_duration":0.1}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := createBody()["speech_model"]; ok {
		t.Errorf("speech_model should be omitted when model id is empty, got %v", createBody()["speech_model"])
	}
}

func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	srv, _, createBody, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"completed","text":"hi","audio_duration":0.1}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := createBody()["language_code"]; ok {
		t.Errorf("language_code should be omitted when Language is empty, got %v", createBody()["language_code"])
	}
}

func TestTranscribe_PollHappyPath(t *testing.T) {
	srv, pollCount, _, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"queued"}`,
		`{"id":"transcript-1","status":"processing"}`,
		`{"id":"transcript-1","status":"completed","text":"done","audio_duration":2}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if resp.Text != "done" {
		t.Errorf("Text = %q", resp.Text)
	}
	if atomic.LoadInt32(pollCount) != 3 {
		t.Errorf("pollCount = %d, want 3", *pollCount)
	}
}

func TestTranscribe_ErrorStatus(t *testing.T) {
	srv, _, _, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"error","error":"audio file is corrupt"}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for error status")
	}
	if !strings.Contains(err.Error(), "audio file is corrupt") {
		t.Errorf("error = %q, want it to include the error field", err.Error())
	}
}

func TestTranscribe_ProviderOptionsMergeTopLevel(t *testing.T) {
	srv, _, createBody, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"completed","text":"hi","audio_duration":0.1}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio: []byte("x"),
		ProviderOptions: map[string]any{
			"assemblyai": map[string]any{
				"speech_model":   "slam-1",
				"speaker_labels": true,
				"punctuate":      false,
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	body := createBody()
	if body["speech_model"] != "slam-1" {
		t.Errorf("speech_model = %v, want override %q", body["speech_model"], "slam-1")
	}
	if body["speaker_labels"] != true {
		t.Errorf("speaker_labels = %v, want true", body["speaker_labels"])
	}
	if body["punctuate"] != false {
		t.Errorf("punctuate = %v, want false", body["punctuate"])
	}
	if _, ok := body["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestTranscribe_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
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
		t.Errorf("Message = %q, want parsed error %q", apiErr.Message, "invalid api key")
	}
}

func TestTranscribe_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upload_url":"https://cdn.assemblyai.test/audio123"}`))
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"transcript-1","status":"queued"}`))
	})
	mux.HandleFunc("/v2/transcript/transcript-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"transcript-1","status":"queued"}`))
		select {
		case pollHit <- struct{}{}:
		default:
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A poll interval long enough that the test can cancel the context
	// while Transcribe is sleeping between polls.
	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(200*time.Millisecond))
	m := p.TranscriptionModel("universal")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-pollHit
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := m.Transcribe(ctx, provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestTranscribe_PollNon2xxError(t *testing.T) {
	var pollHit int32

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upload_url":"https://cdn.assemblyai.test/audio123"}`))
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"transcript-1","status":"queued"}`))
	})
	mux.HandleFunc("/v2/transcript/transcript-1", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal error"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for non-2xx poll response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if got := atomic.LoadInt32(&pollHit); got != 1 {
		t.Errorf("poll hit count = %d, want 1 (loop should exit on first error)", got)
	}
}

func TestTranscribe_EmptyUploadURLError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"upload_url":""}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for empty upload_url")
	}
	if !strings.Contains(err.Error(), "no upload_url") {
		t.Errorf("error = %q, want it to mention the missing upload_url", err.Error())
	}
}

func TestTranscribe_PromptIgnored(t *testing.T) {
	srv, _, createBody, _, _ := newFixtureServer(t, []string{
		`{"id":"transcript-1","status":"completed","text":"hi","audio_duration":0.1}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("universal")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:  []byte("x"),
		Prompt: "some context prompt",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := createBody()["prompt"]; ok {
		t.Errorf("prompt should not be sent, got %v", createBody()["prompt"])
	}
}
