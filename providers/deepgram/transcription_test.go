package deepgram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestTranscribe_HappyPath(t *testing.T) {
	var gotQuery, gotAuth, gotContentType string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = b

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"metadata": {"duration": 1.2},
			"results": {
				"channels": [
					{
						"detected_language": "en",
						"alternatives": [
							{
								"transcript": "hello world",
								"words": [
									{"word":"hello","start":0.0,"end":0.5},
									{"word":"world","start":0.6,"end":1.2}
								]
							}
						]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("test-key"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("fake-audio-bytes"),
		MediaType: "audio/wav",
		Language:  "en",
		ProviderOptions: map[string]any{
			"deepgram": map[string]any{
				"punctuate": true,
				"tier":      "enhanced",
			},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	q, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Get("model") != "nova-3" {
		t.Errorf("model query param = %q", q.Get("model"))
	}
	if q.Get("smart_format") != "true" {
		t.Errorf("smart_format query param = %q", q.Get("smart_format"))
	}
	if q.Get("language") != "en" {
		t.Errorf("language query param = %q", q.Get("language"))
	}
	if q.Get("punctuate") != "true" {
		t.Errorf("punctuate query param = %q", q.Get("punctuate"))
	}
	if q.Get("tier") != "enhanced" {
		t.Errorf("tier query param = %q", q.Get("tier"))
	}

	if gotAuth != "Token test-key" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if gotContentType != "audio/wav" {
		t.Errorf("Content-Type header = %q", gotContentType)
	}
	if string(gotBody) != "fake-audio-bytes" {
		t.Errorf("request body = %q", gotBody)
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
		t.Fatalf("Segments len = %d, want 2", len(resp.Segments))
	}
	if resp.Segments[0].Text != "hello" || resp.Segments[0].StartSec != 0.0 || resp.Segments[0].EndSec != 0.5 {
		t.Errorf("Segments[0] = %+v", resp.Segments[0])
	}
	if resp.Segments[1].Text != "world" || resp.Segments[1].StartSec != 0.6 || resp.Segments[1].EndSec != 1.2 {
		t.Errorf("Segments[1] = %+v", resp.Segments[1])
	}
}

func TestTranscribe_SegmentsPreferPunctuatedWord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"metadata": {"duration": 1.2},
			"results": {
				"channels": [
					{
						"alternatives": [
							{
								"transcript": "Hello, world.",
								"words": [
									{"word":"hello","punctuated_word":"Hello,","start":0.0,"end":0.5},
									{"word":"world","punctuated_word":"world.","start":0.6,"end":1.2}
								]
							}
						]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(resp.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(resp.Segments))
	}
	if resp.Segments[0].Text != "Hello," {
		t.Errorf("Segments[0].Text = %q, want punctuated form %q", resp.Segments[0].Text, "Hello,")
	}
	if resp.Segments[1].Text != "world." {
		t.Errorf("Segments[1].Text = %q, want punctuated form %q", resp.Segments[1].Text, "world.")
	}
}

func TestTranscribe_SegmentsFallBackToWordWhenPunctuatedWordAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"metadata": {"duration": 0.5},
			"results": {
				"channels": [
					{
						"alternatives": [
							{
								"transcript": "hello",
								"words": [
									{"word":"hello","start":0.0,"end":0.5}
								]
							}
						]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	resp, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(resp.Segments) != 1 {
		t.Fatalf("Segments len = %d, want 1", len(resp.Segments))
	}
	if resp.Segments[0].Text != "hello" {
		t.Errorf("Segments[0].Text = %q, want fallback %q", resp.Segments[0].Text, "hello")
	}
}

func TestTranscribe_ProviderOptionsOverrideModelQueryParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"metadata":{"duration":0},"results":{"channels":[{"alternatives":[{"transcript":"hi","words":[]}]}]}}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
		ProviderOptions: map[string]any{
			"deepgram": map[string]any{
				"model": "nova-2",
			},
		},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	q, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Get("model") != "nova-2" {
		t.Errorf("model query param = %q, want provider-options override %q", q.Get("model"), "nova-2")
	}
}

func TestTranscribe_NoLanguageOmitsQueryParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"metadata":{"duration":0},"results":{"channels":[{"alternatives":[{"transcript":"hi","words":[]}]}]}}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	q, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Has("language") {
		t.Errorf("language query param should not be present, got %q", q.Get("language"))
	}
}

func TestTranscribe_DefaultContentType(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"metadata":{"duration":0},"results":{"channels":[{"alternatives":[{"transcript":"hi","words":[]}]}]}}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio: []byte("x"),
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotContentType != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotContentType)
	}
}

func TestTranscribe_EmptyResultsIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"metadata":{"duration":0},"results":{"channels":[]}}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey("k"), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err == nil {
		t.Fatal("expected error for empty results")
	}
}

func TestTranscribe_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"err_msg":"invalid credentials"}`))
	}))
	defer srv.Close()

	p := New(WithAPIKey(""), WithBaseURL(srv.URL))
	m := p.TranscriptionModel("nova-3")

	_, err := m.Transcribe(context.Background(), provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
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
	if apiErr.Message != "invalid credentials" {
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
	m := p.TranscriptionModel("nova-3")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Transcribe(ctx, provider.TranscriptionCall{
		Audio:     []byte("x"),
		MediaType: "audio/wav",
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
