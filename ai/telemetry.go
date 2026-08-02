package ai

import (
	"context"
	"iter"
	"time"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// SpanInfo describes a single Generate or Stream call observed by
// TelemetryMiddleware. Passed to Telemetry.OnSpanStart with only
// Operation/ModelID/ProviderName/StartTime populated (EndTime is the zero
// time, Usage is the zero value, FinishReason is empty, Err is nil), and to
// Telemetry.OnSpanEnd fully populated: EndTime always set, and either
// Usage/FinishReason (on success) or Err (on failure) set — never both.
type SpanInfo struct {
	Operation    string // "generate" | "stream"
	ModelID      string
	ProviderName string
	StartTime    time.Time
	EndTime      time.Time      // zero on Start
	Usage        provider.Usage // zero on Start
	FinishReason provider.FinishReason
	Err          error
}

// Telemetry receives span events from TelemetryMiddleware. Implementations
// must be safe for concurrent use, since a middleware-wrapped model may be
// called concurrently. Adapt to OTel (or any other tracing system) by
// implementing this interface with a tracer: start a span in OnSpanStart,
// stash it (e.g. keyed by a pointer identity or via a field on a
// per-implementation wrapper), and end it in OnSpanEnd.
type Telemetry interface {
	OnSpanStart(info SpanInfo)
	OnSpanEnd(info SpanInfo)
}

// TelemetryMiddleware wraps model so that every Generate and Stream call
// reports a span to t: OnSpanStart when the call begins, OnSpanEnd when it
// ends.
//
// Generate emits exactly one span per call, ending when Generate returns
// (with Usage/FinishReason on success, Err on failure).
//
// Stream emits one span per call that ends when the stream's FinishPart is
// observed during iteration (with Usage/FinishReason taken from it), or —
// if no FinishPart is ever observed — once Parts() iteration ends for any
// other reason: a mid-stream error (StreamResponse.Err(), recorded as Err),
// the consumer abandoning iteration early, or Close being called before
// either of those happens. In every case the span ends with whatever is
// known at that point; nothing is buffered or invented to make an
// abandoned/errored stream look complete. A failure to start the stream
// (model.Stream returning a non-nil error) ends the span immediately with
// that Err, and no StreamResponse is ever wrapped or returned.
func TelemetryMiddleware(model provider.LanguageModel, t Telemetry) provider.LanguageModel {
	return &telemetryModel{model: model, t: t}
}

type telemetryModel struct {
	model provider.LanguageModel
	t     Telemetry
}

func (m *telemetryModel) ModelID() string                     { return m.model.ModelID() }
func (m *telemetryModel) ProviderName() string                { return m.model.ProviderName() }
func (m *telemetryModel) Capabilities() provider.Capabilities { return m.model.Capabilities() }

func (m *telemetryModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	info := SpanInfo{
		Operation:    "generate",
		ModelID:      m.model.ModelID(),
		ProviderName: m.model.ProviderName(),
		StartTime:    time.Now(),
	}
	m.t.OnSpanStart(info)

	resp, err := m.model.Generate(ctx, call)
	info.EndTime = time.Now()
	if err != nil {
		info.Err = err
		m.t.OnSpanEnd(info)
		return nil, err
	}
	info.Usage = resp.Usage
	info.FinishReason = resp.FinishReason
	m.t.OnSpanEnd(info)
	return resp, nil
}

func (m *telemetryModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	info := SpanInfo{
		Operation:    "stream",
		ModelID:      m.model.ModelID(),
		ProviderName: m.model.ProviderName(),
		StartTime:    time.Now(),
	}
	m.t.OnSpanStart(info)

	inner, err := m.model.Stream(ctx, call)
	if err != nil {
		info.EndTime = time.Now()
		info.Err = err
		m.t.OnSpanEnd(info)
		return nil, err
	}
	return &telemetryStream{inner: inner, t: m.t, info: info}, nil
}

// telemetryStream wraps a provider.StreamResponse so the span in info is
// ended exactly once, as soon as enough is known: at the FinishPart (if
// one is observed), otherwise at the end of iteration (error, abandonment,
// or natural close-without-finish), whichever happens first.
//
// Like the rest of this codebase, telemetryStream assumes StreamResponse
// has a single consumer driving Parts() to completion; it does not
// synchronize s.ended/s.info against a concurrent Close() call made from
// another goroutine while Parts() is still being iterated.
type telemetryStream struct {
	inner provider.StreamResponse
	t     Telemetry
	info  SpanInfo
	ended bool
}

// endSpan reports the span's end exactly once; later calls are no-ops so
// callers don't need to track whether an earlier code path already ended
// it.
func (s *telemetryStream) endSpan() {
	if s.ended {
		return
	}
	s.ended = true
	s.info.EndTime = time.Now()
	s.t.OnSpanEnd(s.info)
}

func (s *telemetryStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for p := range s.inner.Parts() {
			if fp, ok := p.(provider.FinishPart); ok {
				s.info.Usage = fp.Usage
				s.info.FinishReason = fp.Reason
				s.endSpan()
			}
			if !yield(p) {
				// Consumer abandoned iteration. If a FinishPart was already
				// observed, the span already ended with that info above and
				// this is a no-op; otherwise end it now with whatever is
				// known (no error yet — the underlying stream never got a
				// chance to report one).
				s.endSpan()
				return
			}
		}
		// Parts() exhausted without the consumer abandoning it. If no
		// FinishPart was observed, this is either a mid-stream error or a
		// stream that ended without ever emitting one; either way, end the
		// span now, capturing Err() if the stream reports one.
		if !s.ended {
			s.info.Err = s.inner.Err()
			s.endSpan()
		}
	}
}

func (s *telemetryStream) Err() error { return s.inner.Err() }

func (s *telemetryStream) Close() error {
	err := s.inner.Close()
	if !s.ended {
		s.info.Err = s.inner.Err()
		s.endSpan()
	}
	return err
}
