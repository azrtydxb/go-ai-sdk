package gladia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// newFixtureServer wires up the three Gladia endpoints used by the async
// transcription flow: upload, create, poll. pollBodies is served in order,
// one body per poll request; the last body is repeated if there are more
// poll requests than bodies.
func newFixtureServer(t *testing.T, pollBodies []string) (srv *httptest.Server, pollCount *int32, createBody func() map[string]any, uploadKey func() string, createKey func() string, uploadFilename func() string) {
	t.Helper()

	var n int32
	var gotUploadKey, gotCreateKey, gotFilename string
	var gotCreateBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		gotUploadKey = r.Header.Get("x-gladia-key")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				if part.FormName() == "audio" {
					gotFilename = part.FileName()
					io.ReadAll(part)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"audio_url":"https://cdn.gladia.test/audio123"}`))
	})
	mux.HandleFunc("/v2/pre-recorded", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		gotCreateKey = r.Header.Get("x-gladia-key")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotCreateBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","result_url":"https://api.gladia.test/v2/pre-recorded/job-1"}`))
	})
	mux.HandleFunc("/v2/pre-recorded/job-1", func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt32(&n, 1)
		idx := int(i) - 1
		if idx >= len(pollBodies) {
			idx = len(pollBodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(pollBodies[idx]))
	})

	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)

	return s, &n,
		func() map[string]any { return gotCreateBody },
		func() string { return gotUploadKey },
		func() string { return gotCreateKey },
		func() string { return gotFilename }
}

func TestTranscribe_RequestShapeAndSegments(t *testing.T) {
	const done = `{"id":"job-1","status":"done","result":{"metadata":{"audio_duration":1.2},"transcription":{"full_transcript":"hello world","utterances":[{"text":"hello","start":0,"end":0.5},{"text":"world","start":0.5,"end":1.2}]}}}`
	srv, pollCount, createBody, uploadKey, createKey, uploadFilename := newFixtureServer(t, []string{done})

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if uploadKey() != "test-key" {
		t.Errorf("upload x-gladia-key = %q, want %q", uploadKey(), "test-key")
	}
	if createKey() != "test-key" {
		t.Errorf("create x-gladia-key = %q, want %q", createKey(), "test-key")
	}
	if uploadFilename() != "audio.mp3" {
		t.Errorf("upload filename = %q, want %q", uploadFilename(), "audio.mp3")
	}

	body := createBody()
	if body["audio_url"] != "https://cdn.gladia.test/audio123" {
		t.Errorf("audio_url = %v", body["audio_url"])
	}
	langConfig, ok := body["language_config"].(map[string]any)
	if !ok {
		t.Fatalf("language_config missing or wrong type: %v", body["language_config"])
	}
	langs, ok := langConfig["languages"].([]any)
	if !ok || len(langs) != 1 || langs[0] != "en" {
		t.Errorf("language_config.languages = %v, want [\"en\"]", langConfig["languages"])
	}

	if resp.Text != "hello world" {
		t.Errorf("Text = %q", resp.Text)
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

func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	const done = `{"id":"job-1","status":"done","result":{"metadata":{"audio_duration":0.1},"transcription":{"full_transcript":"hi"}}}`
	srv, _, createBody, _, _, _ := newFixtureServer(t, []string{done})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := createBody()["language_config"]; ok {
		t.Errorf("language_config should be omitted when Language is empty, got %v", createBody()["language_config"])
	}
}

func TestTranscribe_PollHappyPath(t *testing.T) {
	srv, pollCount, _, _, _, _ := newFixtureServer(t, []string{
		`{"id":"job-1","status":"queued"}`,
		`{"id":"job-1","status":"processing"}`,
		`{"id":"job-1","status":"done","result":{"metadata":{"audio_duration":2},"transcription":{"full_transcript":"done"}}}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

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
	srv, _, _, _, _, _ := newFixtureServer(t, []string{
		`{"id":"job-1","status":"error","error_code":"corrupt_file"}`,
	})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for error status")
	}
	if !strings.Contains(err.Error(), "corrupt_file") {
		t.Errorf("error = %q, want it to include the error_code field", err.Error())
	}
}

func TestTranscribe_ProviderOptionsMergeTopLevel(t *testing.T) {
	const done = `{"id":"job-1","status":"done","result":{"metadata":{"audio_duration":0.1},"transcription":{"full_transcript":"hi"}}}`
	srv, _, createBody, _, _, _ := newFixtureServer(t, []string{done})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio: []byte("x"),
		ProviderOptions: map[string]any{
			"gladia": map[string]any{
				"diarization":     true,
				"custom_metadata": map[string]any{"foo": "bar"},
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	body := createBody()
	if body["diarization"] != true {
		t.Errorf("diarization = %v, want true", body["diarization"])
	}
	if _, ok := body["custom_metadata"]; !ok {
		t.Errorf("custom_metadata missing")
	}
	if _, ok := body["other-provider"]; ok {
		t.Error("other-provider options should not leak into request body")
	}
}

func TestTranscribe_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("")

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
		t.Errorf("Message = %q, want parsed message %q", apiErr.Message, "invalid api key")
	}
}

func TestTranscribe_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"audio_url":"https://cdn.gladia.test/audio123"}`))
	})
	mux.HandleFunc("/v2/pre-recorded", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","result_url":"x"}`))
	})
	mux.HandleFunc("/v2/pre-recorded/job-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","status":"queued"}`))
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
	m := p.TranscriptionModel("")

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
		w.Write([]byte(`{"audio_url":"https://cdn.gladia.test/audio123"}`))
	})
	mux.HandleFunc("/v2/pre-recorded", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","result_url":"x"}`))
	})
	mux.HandleFunc("/v2/pre-recorded/job-1", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"internal error"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

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

func TestTranscribe_EmptyAudioURLError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"audio_url":""}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for empty audio_url")
	}
	if !strings.Contains(err.Error(), "no audio_url") {
		t.Errorf("error = %q, want it to mention the missing audio_url", err.Error())
	}
}

func TestTranscribe_PromptIgnored(t *testing.T) {
	const done = `{"id":"job-1","status":"done","result":{"metadata":{"audio_duration":0.1},"transcription":{"full_transcript":"hi"}}}`
	srv, _, createBody, _, _, _ := newFixtureServer(t, []string{done})

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

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
