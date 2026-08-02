package openaicompat

// Request-shape tests for NewTranscriptionModel, white-box (package
// openaicompat). The wire body is multipart/form-data, so tests re-parse
// the recorded raw request body with mime/multipart, using the recorded
// Content-Type header to recover the boundary.

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// parseMultipart parses the last recorded request as multipart/form-data,
// using contentType (the recorded Content-Type header value) to recover the
// boundary. It returns the file part's filename, its Content-Type, its
// contents, and the plain form fields.
func parseMultipart(t *testing.T, raw []byte, contentType string) (filename, fileContentType string, fileData []byte, fields map[string]string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content-type %q: %v", contentType, err)
	}
	boundary, ok := params["boundary"]
	if !ok {
		t.Fatalf("content-type %q missing boundary", contentType)
	}

	fields = make(map[string]string)
	mr := multipart.NewReader(bytes.NewReader(raw), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part data: %v", err)
		}
		if part.FormName() == "file" {
			filename = part.FileName()
			fileContentType = part.Header.Get("Content-Type")
			fileData = data
			continue
		}
		fields[part.FormName()] = string(data)
	}
	return filename, fileContentType, fileData, fields
}

func TestTranscriptionRequestShapeWhisperVerboseJSON(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewTranscriptionModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")

	resp, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/mpeg",
		Language:  "en",
		Prompt:    "a hint",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	ct := srv.HeaderValues("Content-Type")[0]
	filename, fileCT, fileData, fields := parseMultipart(t, reqs[0], ct)

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
	if fields["language"] != "en" {
		t.Errorf("language field = %q, want en", fields["language"])
	}
	if fields["prompt"] != "a hint" {
		t.Errorf("prompt field = %q, want 'a hint'", fields["prompt"])
	}
	if fields["response_format"] != "verbose_json" {
		t.Errorf("response_format field = %q, want verbose_json", fields["response_format"])
	}

	// Fixture returns verbose_json shape: text/language/duration/segments.
	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", resp.Text)
	}
	if resp.Language != "en" {
		t.Errorf("Language = %q, want en", resp.Language)
	}
	if resp.DurationSec != 1.5 {
		t.Errorf("DurationSec = %v, want 1.5", resp.DurationSec)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(resp.Segments))
	}
	if resp.Segments[0].Text != "hello" || resp.Segments[0].StartSec != 0 || resp.Segments[0].EndSec != 0.5 {
		t.Errorf("Segments[0] = %+v, want {hello 0 0.5}", resp.Segments[0])
	}
	if resp.Segments[1].Text != "world" || resp.Segments[1].StartSec != 0.5 || resp.Segments[1].EndSec != 1.5 {
		t.Errorf("Segments[1] = %+v, want {world 0.5 1.5}", resp.Segments[1])
	}
}

func TestTranscriptionRequestShapeGPT4oUsesJSON(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewTranscriptionModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "gpt-4o-transcribe")

	resp, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("raw-audio-bytes"),
		MediaType: "audio/wav",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	reqs := srv.Requests()
	ct := srv.HeaderValues("Content-Type")[0]
	filename, fileCT, _, fields := parseMultipart(t, reqs[0], ct)

	if filename != "audio.wav" {
		t.Errorf("filename = %q, want audio.wav", filename)
	}
	if fileCT != "audio/wav" {
		t.Errorf("file Content-Type = %q, want audio/wav", fileCT)
	}
	if fields["response_format"] != "json" {
		t.Errorf("response_format field = %q, want json", fields["response_format"])
	}
	if _, ok := fields["language"]; ok {
		t.Errorf("language field unexpectedly present: %v", fields)
	}
	if _, ok := fields["prompt"]; ok {
		t.Errorf("prompt field unexpectedly present: %v", fields)
	}

	// Response is still parsed correctly even though the fixture always
	// returns the verbose_json shape; the json-shape parse path only
	// reads the "text" field, which is a subset of verbose_json's fields.
	if resp.Text != "hello world" {
		t.Errorf("Text = %q, want hello world", resp.Text)
	}
}

func TestTranscriptionExtensionMapping(t *testing.T) {
	cases := []struct {
		mediaType string
		want      string
	}{
		{"audio/mpeg", "mp3"},
		{"audio/wav", "wav"},
		{"audio/mp4", "mp4"},
		{"audio/webm", "webm"},
		{"audio/unknown-format", "bin"},
	}
	for _, tc := range cases {
		t.Run(tc.mediaType, func(t *testing.T) {
			srv := compattest.NewFixtureServer(t, "test")
			model := NewTranscriptionModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL}, "whisper-1")
			_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{
				Audio:     []byte("x"),
				MediaType: tc.mediaType,
			})
			if err != nil {
				t.Fatalf("Transcribe: %v", err)
			}
			reqs := srv.Requests()
			ct := srv.HeaderValues("Content-Type")[len(srv.HeaderValues("Content-Type"))-1]
			filename, _, _, _ := parseMultipart(t, reqs[len(reqs)-1], ct)
			want := "audio." + tc.want
			if filename != want {
				t.Errorf("mediaType %s: filename = %q, want %q", tc.mediaType, filename, want)
			}
		})
	}
}

func TestTranscriptionEmptyBaseURLErrors(t *testing.T) {
	model := NewTranscriptionModel(Config{Name: "test", APIKey: "k"}, "whisper-1")
	_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err == nil {
		t.Fatal("Transcribe: want error for empty BaseURL, got nil")
	}
}

func TestTranscriptionAuthHeader(t *testing.T) {
	srv := compattest.NewFixtureServer(t, "test")
	model := NewTranscriptionModel(Config{Name: "test", APIKey: "k", BaseURL: srv.URL, APIKeyHeader: "api-key"}, "whisper-1")

	_, err := model.Transcribe(context.Background(), provider.TranscriptionCall{Audio: []byte("x"), MediaType: "audio/mpeg"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got := srv.HeaderValues("api-key"); len(got) != 1 || got[0] != "k" {
		t.Errorf("api-key header = %v, want [k]", got)
	}
	if got := srv.HeaderValues("Authorization"); len(got) != 1 || got[0] != "" {
		t.Errorf("Authorization header = %v, want [\"\"]", got)
	}
}
