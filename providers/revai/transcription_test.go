package revai

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

// jobFixture wires up the three Rev.ai endpoints used by the async
// transcription flow: create job, poll job, fetch transcript. transcript is
// the body returned once the job is polled as "transcribed".
type jobFixture struct {
	pollBodies []string
	transcript string

	pollCount        int32
	createAuth       string
	createOptions    map[string]any
	createFilename   string
	transcriptAccept string
}

func newJobFixture(t *testing.T, pollBodies []string, transcript string) (*httptest.Server, *jobFixture) {
	t.Helper()
	f := &jobFixture{pollBodies: pollBodies, transcript: transcript}

	mux := http.NewServeMux()
	mux.HandleFunc("/speechtotext/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		f.createAuth = r.Header.Get("Authorization")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				switch part.FormName() {
				case "media":
					f.createFilename = part.FileName()
					io.ReadAll(part)
				case "options":
					b, _ := io.ReadAll(part)
					json.Unmarshal(b, &f.createOptions)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","status":"in_progress"}`))
	})
	mux.HandleFunc("/speechtotext/v1/jobs/job-1", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&f.pollCount, 1)
		idx := int(n) - 1
		if idx >= len(f.pollBodies) {
			idx = len(f.pollBodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(f.pollBodies[idx]))
	})
	mux.HandleFunc("/speechtotext/v1/jobs/job-1/transcript", func(w http.ResponseWriter, r *http.Request) {
		f.transcriptAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/vnd.rev.transcript.v1.0+json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(f.transcript))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestTranscribe_RequestShapeAndTranscriptParsing(t *testing.T) {
	transcript := `{"monologues":[{"speaker":0,"elements":[` +
		`{"type":"text","value":"Hello","ts":0.0,"end_ts":0.5},` +
		`{"type":"punct","value":" "},` +
		`{"type":"text","value":"world","ts":0.5,"end_ts":1.2},` +
		`{"type":"punct","value":"."}` +
		`]}]}`
	srv, f := newJobFixture(t, []string{`{"id":"job-1","status":"transcribed"}`}, transcript)

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/wav",
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if f.createAuth != "Bearer test-key" {
		t.Errorf("create Authorization = %q, want %q", f.createAuth, "Bearer test-key")
	}
	if f.createFilename != "audio.wav" {
		t.Errorf("create media filename = %q, want %q", f.createFilename, "audio.wav")
	}
	if f.createOptions["language"] != "en" {
		t.Errorf("options.language = %v, want %q", f.createOptions["language"], "en")
	}
	if f.transcriptAccept != "application/vnd.rev.transcript.v1.0+json" {
		t.Errorf("transcript Accept = %q, want the vnd.rev.transcript header", f.transcriptAccept)
	}

	if resp.Text != "Hello world." {
		t.Errorf("Text = %q, want %q", resp.Text, "Hello world.")
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments = %+v, want 2 (punct elements excluded)", resp.Segments)
	}
	if resp.Segments[0].Text != "Hello" || resp.Segments[0].StartSec != 0 || resp.Segments[0].EndSec != 0.5 {
		t.Errorf("segment 0 = %+v", resp.Segments[0])
	}
	if resp.Segments[1].Text != "world" || resp.Segments[1].StartSec != 0.5 || resp.Segments[1].EndSec != 1.2 {
		t.Errorf("segment 1 = %+v", resp.Segments[1])
	}
	if resp.DurationSec != 1.2 {
		t.Errorf("DurationSec = %v, want 1.2 (last end_ts)", resp.DurationSec)
	}
}

func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	transcript := `{"monologues":[{"elements":[{"type":"text","value":"hi","ts":0,"end_ts":0.1}]}]}`
	srv, f := newJobFixture(t, []string{`{"id":"job-1","status":"transcribed"}`}, transcript)

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := f.createOptions["language"]; ok {
		t.Errorf("language should be omitted when empty, got %v", f.createOptions["language"])
	}
}

func TestTranscribe_ProviderOptionsMergeIntoOptions(t *testing.T) {
	transcript := `{"monologues":[{"elements":[{"type":"text","value":"hi","ts":0,"end_ts":0.1}]}]}`
	srv, f := newJobFixture(t, []string{`{"id":"job-1","status":"transcribed"}`}, transcript)

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:    []byte("x"),
		Language: "en",
		ProviderOptions: map[string]any{
			"revai": map[string]any{
				"language":             "fr",
				"custom_vocabulary_id": "vocab-1",
			},
			"other-provider": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if f.createOptions["language"] != "fr" {
		t.Errorf("language = %v, want provider-option override %q", f.createOptions["language"], "fr")
	}
	if f.createOptions["custom_vocabulary_id"] != "vocab-1" {
		t.Errorf("custom_vocabulary_id = %v, want %q", f.createOptions["custom_vocabulary_id"], "vocab-1")
	}
	if _, ok := f.createOptions["other-provider"]; ok {
		t.Error("other-provider options should not leak into the options JSON")
	}
}

func TestTranscribe_PollHappyPath(t *testing.T) {
	transcript := `{"monologues":[{"elements":[{"type":"text","value":"done","ts":0,"end_ts":2}]}]}`
	srv, f := newJobFixture(t, []string{
		`{"id":"job-1","status":"in_progress"}`,
		`{"id":"job-1","status":"in_progress"}`,
		`{"id":"job-1","status":"transcribed"}`,
	}, transcript)

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if resp.Text != "done" {
		t.Errorf("Text = %q", resp.Text)
	}
	if atomic.LoadInt32(&f.pollCount) != 3 {
		t.Errorf("pollCount = %d, want 3", f.pollCount)
	}
}

func TestTranscribe_FailedStatus(t *testing.T) {
	srv, _ := newJobFixture(t, []string{
		`{"id":"job-1","status":"failed","failure_detail":"invalid media format"}`,
	}, "")

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for failed status")
	}
	if !strings.Contains(err.Error(), "invalid media format") {
		t.Errorf("error = %q, want it to include failure_detail", err.Error())
	}
}

// TestTranscribe_FailedStatus_EmptyFailureDetailFallsBackToBody covers the
// fallback for a terminal "failed" status whose failure_detail field is
// empty: the raw poll response body is included in the returned error
// instead of a bare "revai: job ... failed" with no detail at all,
// mirroring assemblyai's equivalent fallback.
func TestTranscribe_FailedStatus_EmptyFailureDetailFallsBackToBody(t *testing.T) {
	srv, _ := newJobFixture(t, []string{
		`{"id":"job-1","status":"failed","distinctive_debug_field":"xyzzy-plugh"}`,
	}, "")

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	if err == nil {
		t.Fatal("expected error for failed status")
	}
	if !strings.Contains(err.Error(), "xyzzy-plugh") {
		t.Errorf("error = %q, want it to include the raw response body since failure_detail is empty", err.Error())
	}
}

func TestTranscribe_401Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"title":"Unauthorized","detail":"invalid access token"}`))
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
	if apiErr.Message != "invalid access token" {
		t.Errorf("Message = %q, want parsed detail %q", apiErr.Message, "invalid access token")
	}
}

func TestTranscribe_401Error_TitleFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"title":"Unauthorized"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("bad-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x")})
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *ai.APICallError: %v (%T)", err, err)
	}
	if apiErr.Message != "Unauthorized" {
		t.Errorf("Message = %q, want title fallback %q", apiErr.Message, "Unauthorized")
	}
}

func TestTranscribe_ContextCancellationMidPoll(t *testing.T) {
	pollHit := make(chan struct{}, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/speechtotext/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","status":"in_progress"}`))
	})
	mux.HandleFunc("/speechtotext/v1/jobs/job-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","status":"in_progress"}`))
		select {
		case pollHit <- struct{}{}:
		default:
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

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
	mux.HandleFunc("/speechtotext/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"job-1","status":"in_progress"}`))
	})
	mux.HandleFunc("/speechtotext/v1/jobs/job-1", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pollHit, 1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"title":"internal error"}`))
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

func TestTranscribe_PromptIgnored(t *testing.T) {
	transcript := `{"monologues":[{"elements":[{"type":"text","value":"hi","ts":0,"end_ts":0.1}]}]}`
	srv, f := newJobFixture(t, []string{`{"id":"job-1","status":"transcribed"}`}, transcript)

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL), WithPollInterval(time.Millisecond))
	m := p.TranscriptionModel("")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:  []byte("x"),
		Prompt: "some context prompt",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if _, ok := f.createOptions["prompt"]; ok {
		t.Errorf("prompt should not be sent, got %v", f.createOptions["prompt"])
	}
}

func TestNew_EnvKeyFallback(t *testing.T) {
	t.Setenv("REVAI_API_KEY", "")
	t.Setenv("REV_AI_API_KEY", "fallback-key")

	p := New()
	if p.apiKey != "fallback-key" {
		t.Errorf("apiKey = %q, want fallback env var value %q", p.apiKey, "fallback-key")
	}
}

func TestNew_EnvKeyPrimary(t *testing.T) {
	t.Setenv("REVAI_API_KEY", "primary-key")
	t.Setenv("REV_AI_API_KEY", "fallback-key")

	p := New()
	if p.apiKey != "primary-key" {
		t.Errorf("apiKey = %q, want primary env var value %q", p.apiKey, "primary-key")
	}
}

// --- Security: multipart CRLF/quote injection guard ---
//
// mime/multipart.Writer writes MIME headers verbatim with no CRLF
// validation (unlike net/http), so a caller-supplied MediaType could
// otherwise forge extra multipart headers or parts via the "media" part's
// Content-Type. This test confirms such a value is rejected before
// anything is sent to the server.

func TestTranscribe_MediaTypeCRLFRejected(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	model := p.TranscriptionModel("machine")

	_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("audio"),
		MediaType: "audio/mpeg\r\nX-Injected: 1",
	})
	if err == nil {
		t.Fatal("Transcribe: want error for MediaType containing CRLF")
	}
	if hit {
		t.Error("Transcribe: request was sent despite invalid MediaType")
	}
}
