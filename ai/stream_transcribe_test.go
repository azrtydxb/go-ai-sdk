package ai

import (
	"context"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestStreamTranscribeNilModel(t *testing.T) {
	_, err := StreamTranscribe(t.Context(), StreamTranscribeOpts{})
	if !errors.Is(err, ErrModelRequired) {
		t.Fatalf("err = %v, want ErrModelRequired", err)
	}
}

func TestStreamTranscribePassthrough(t *testing.T) {
	m := &aitest.MockStreamingTranscriptionModel{
		Events: []provider.TranscriptEvent{
			{Text: "hel", Final: false},
			{Text: "hello", Final: true, StartSec: 0, EndSec: 0.5},
		},
	}
	stream, err := StreamTranscribe(t.Context(), StreamTranscribeOpts{
		Model:      m,
		MediaType:  "audio/pcm;rate=16000",
		Language:   "en",
		SampleRate: 16000,
		ProviderOptions: map[string]any{
			"deepgram": map[string]any{"foo": "bar"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.Calls))
	}
	call := m.Calls[0]
	if call.MediaType != "audio/pcm;rate=16000" || call.Language != "en" || call.SampleRate != 16000 {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if opts, ok := call.ProviderOptions["deepgram"].(map[string]any); !ok || opts["foo"] != "bar" {
		t.Fatalf("ProviderOptions not passed through: %+v", call.ProviderOptions)
	}

	var got []provider.TranscriptEvent
	for e := range stream.Events() {
		got = append(got, e)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
	if len(got) != 2 || got[0].Text != "hel" || got[1].Final != true {
		t.Fatalf("events mismatch: %+v", got)
	}

	if err := stream.Send(t.Context(), []byte("audio")); err != nil {
		t.Fatal(err)
	}
	if len(m.Sent) != 1 || string(m.Sent[0]) != "audio" {
		t.Fatalf("Sent = %+v", m.Sent)
	}
	if err := stream.CloseSend(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.CloseSent != 1 {
		t.Fatalf("CloseSent = %d, want 1", m.CloseSent)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	// Close is idempotent.
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStreamTranscribeModelErr(t *testing.T) {
	wantErr := errors.New("dial failed")
	m := &aitest.MockStreamingTranscriptionModel{Err: wantErr}
	_, err := StreamTranscribe(t.Context(), StreamTranscribeOpts{Model: m})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestStreamTranscribeStreamErr(t *testing.T) {
	wantErr := errors.New("connection dropped")
	m := &aitest.MockStreamingTranscriptionModel{
		Events:    []provider.TranscriptEvent{{Text: "hi"}},
		StreamErr: wantErr,
	}
	stream, err := StreamTranscribe(t.Context(), StreamTranscribeOpts{Model: m})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
	}
	if !errors.Is(stream.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", stream.Err(), wantErr)
	}
}

// TestMockTranscriptionStreamConcurrentSendAndEvents exercises the
// documented concurrency contract (one goroutine may Send while another
// ranges over Events) against the mock, under -race.
func TestMockTranscriptionStreamConcurrentSendAndEvents(t *testing.T) {
	events := make([]provider.TranscriptEvent, 50)
	for i := range events {
		events[i] = provider.TranscriptEvent{Text: "x"}
	}
	m := &aitest.MockStreamingTranscriptionModel{Events: events}
	stream, err := StreamTranscribe(t.Context(), StreamTranscribeOpts{Model: m})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			_ = stream.Send(context.Background(), []byte{byte(i)})
		}
	}()

	count := 0
	for range stream.Events() {
		count++
	}
	<-done
	if count != 50 {
		t.Fatalf("count = %d, want 50", count)
	}
}
