package ai

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// recordingTelemetry is a Telemetry test double that records every
// OnSpanStart/OnSpanEnd call, safe for concurrent use per the Telemetry
// contract.
type recordingTelemetry struct {
	mu     sync.Mutex
	starts []SpanInfo
	ends   []SpanInfo
}

func (r *recordingTelemetry) OnSpanStart(info SpanInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts = append(r.starts, info)
}

func (r *recordingTelemetry) OnSpanEnd(info SpanInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ends = append(r.ends, info)
}

func TestTelemetryMiddlewareGenerateSpanSuccess(t *testing.T) {
	m := &aitest.MockModel{Responses: []*provider.Response{{
		Content:      []provider.ContentPart{provider.TextPart{Text: "hello"}},
		FinishReason: provider.FinishStop,
		Usage:        provider.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4},
	}}}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	resp, err := wrapped.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "hello" {
		t.Fatalf("Text() = %q", resp.Text())
	}

	if len(tel.starts) != 1 || len(tel.ends) != 1 {
		t.Fatalf("starts=%d ends=%d, want 1 each", len(tel.starts), len(tel.ends))
	}
	start := tel.starts[0]
	if start.Operation != "generate" {
		t.Errorf("start.Operation = %q, want %q", start.Operation, "generate")
	}
	if start.ModelID != m.ModelID() || start.ProviderName != m.ProviderName() {
		t.Errorf("start ModelID/ProviderName = %q/%q, want %q/%q", start.ModelID, start.ProviderName, m.ModelID(), m.ProviderName())
	}
	if start.StartTime.IsZero() {
		t.Error("start.StartTime is zero")
	}
	if !start.EndTime.IsZero() {
		t.Error("start.EndTime should be zero on OnSpanStart")
	}

	end := tel.ends[0]
	if end.EndTime.IsZero() {
		t.Error("end.EndTime is zero")
	}
	if end.EndTime.Before(end.StartTime) {
		t.Errorf("end.EndTime %v before StartTime %v", end.EndTime, end.StartTime)
	}
	if end.Usage != (provider.Usage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4}) {
		t.Errorf("end.Usage = %+v", end.Usage)
	}
	if end.FinishReason != provider.FinishStop {
		t.Errorf("end.FinishReason = %q, want %q", end.FinishReason, provider.FinishStop)
	}
	if end.Err != nil {
		t.Errorf("end.Err = %v, want nil", end.Err)
	}
}

func TestTelemetryMiddlewareGenerateSpanFailure(t *testing.T) {
	wantErr := errors.New("boom")
	m := &aitest.MockModel{Err: wantErr}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	_, err := wrapped.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}

	if len(tel.ends) != 1 {
		t.Fatalf("ends=%d, want 1", len(tel.ends))
	}
	end := tel.ends[0]
	if !errors.Is(end.Err, wantErr) {
		t.Errorf("end.Err = %v, want %v", end.Err, wantErr)
	}
	if end.Usage != (provider.Usage{}) {
		t.Errorf("end.Usage = %+v, want zero", end.Usage)
	}
	if end.FinishReason != "" {
		t.Errorf("end.FinishReason = %q, want empty", end.FinishReason)
	}
}

func TestTelemetryMiddlewareStreamSpanEndsAtFinishPart(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hel"},
		provider.TextDelta{Text: "lo"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}}}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	sr, err := wrapped.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}

	sawFinish := false
	for p := range sr.Parts() {
		if _, ok := p.(provider.FinishPart); ok {
			sawFinish = true
			// The span must have already ended by the time the consumer
			// observes the FinishPart it was yielded from — i.e. ending is
			// driven by observing FinishPart during iteration, not by
			// Parts() returning afterward.
			tel.mu.Lock()
			endsAtFinish := len(tel.ends)
			tel.mu.Unlock()
			if endsAtFinish != 1 {
				t.Fatalf("at FinishPart delivery, len(tel.ends) = %d, want 1 (span should already have ended)", endsAtFinish)
			}
		}
	}
	if !sawFinish {
		t.Fatal("never observed FinishPart")
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}

	if len(tel.ends) != 1 {
		t.Fatalf("ends=%d, want exactly 1 (idempotent end)", len(tel.ends))
	}
	end := tel.ends[0]
	if end.Operation != "stream" {
		t.Errorf("end.Operation = %q, want %q", end.Operation, "stream")
	}
	if end.Usage != (provider.Usage{TotalTokens: 4}) {
		t.Errorf("end.Usage = %+v", end.Usage)
	}
	if end.FinishReason != provider.FinishStop {
		t.Errorf("end.FinishReason = %q, want %q", end.FinishReason, provider.FinishStop)
	}
	if end.Err != nil {
		t.Errorf("end.Err = %v, want nil", end.Err)
	}
	if end.EndTime.Before(end.StartTime) {
		t.Errorf("end.EndTime %v before StartTime %v", end.EndTime, end.StartTime)
	}

	// Close after full consumption must not end the span a second time.
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if len(tel.ends) != 1 {
		t.Fatalf("ends after Close() = %d, want still 1", len(tel.ends))
	}
}

// TestTelemetryMiddlewareStreamSpanEndsWithErrOnTruncation covers a stream
// that is truncated mid-flight — some parts arrive, then the underlying
// stream reports a mid-stream error via Err() with no FinishPart ever
// observed. The span must still end (not hang open forever), reporting the
// error rather than any usage/finish reason.
func TestTelemetryMiddlewareStreamSpanEndsWithErrOnTruncation(t *testing.T) {
	wantErr := errors.New("connection reset mid-stream")
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "par"},
			provider.TextDelta{Text: "tial"},
		}},
		StreamErrs: []error{wantErr},
	}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	sr, err := wrapped.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			text += d.Text
		}
	}
	if text != "partial" {
		t.Fatalf("text = %q, want %q", text, "partial")
	}
	if !errors.Is(sr.Err(), wantErr) {
		t.Fatalf("Err() = %v, want %v", sr.Err(), wantErr)
	}

	if len(tel.ends) != 1 {
		t.Fatalf("ends=%d, want 1", len(tel.ends))
	}
	end := tel.ends[0]
	if !errors.Is(end.Err, wantErr) {
		t.Errorf("end.Err = %v, want %v", end.Err, wantErr)
	}
	if end.FinishReason != "" {
		t.Errorf("end.FinishReason = %q, want empty (no FinishPart observed)", end.FinishReason)
	}
	if end.Usage != (provider.Usage{}) {
		t.Errorf("end.Usage = %+v, want zero (no FinishPart observed)", end.Usage)
	}
}

func TestTelemetryMiddlewareStreamSpanEndsOnCloseBeforeFinish(t *testing.T) {
	m := &aitest.MockModel{Streams: [][]provider.StreamPart{{
		provider.TextDelta{Text: "hi"},
		provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 4}},
	}}}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	sr, err := wrapped.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Close without ever ranging over Parts(): the span should still end,
	// with whatever is known (nothing, since no part was ever observed).
	if err := sr.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if len(tel.ends) != 1 {
		t.Fatalf("ends=%d, want 1", len(tel.ends))
	}
	end := tel.ends[0]
	if end.FinishReason != "" || end.Usage != (provider.Usage{}) || end.Err != nil {
		t.Errorf("end = %+v, want zero Usage/FinishReason/Err since nothing was observed", end)
	}
}

func TestTelemetryMiddlewareStreamStartFailure(t *testing.T) {
	wantErr := errors.New("start failed")
	m := &aitest.MockModel{Err: wantErr}
	tel := &recordingTelemetry{}
	wrapped := TelemetryMiddleware(m, tel)

	_, err := wrapped.Stream(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("hi")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if len(tel.starts) != 1 || len(tel.ends) != 1 {
		t.Fatalf("starts=%d ends=%d, want 1 each", len(tel.starts), len(tel.ends))
	}
	if !errors.Is(tel.ends[0].Err, wantErr) {
		t.Errorf("end.Err = %v, want %v", tel.ends[0].Err, wantErr)
	}
}
