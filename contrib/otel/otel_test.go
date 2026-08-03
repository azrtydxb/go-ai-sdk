package otel_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	otelbridge "github.com/azrtydxb/go-ai-sdk/contrib/otel"
	"github.com/azrtydxb/go-ai-sdk/provider"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newTracerProvider returns a TracerProvider wired to an in-memory
// SpanRecorder so tests can inspect finished spans without any network or
// external collector.
func newTracerProvider() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return tp, sr
}

func attrValue(span sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestGenerate_OneSpan(t *testing.T) {
	tp, sr := newTracerProvider()
	bridge := otelbridge.New(otelbridge.WithTracer(tp.Tracer("test")))

	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{
				FinishReason: provider.FinishStop,
				Usage:        provider.Usage{InputTokens: 10, OutputTokens: 20},
			},
		},
	}
	model := ai.TelemetryMiddleware(mock, bridge)

	_, err := model.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	ended := sr.Ended()
	if len(ended) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(ended))
	}
	span := ended[0]

	wantName := "chat " + mock.ModelID()
	if span.Name() != wantName {
		t.Errorf("span name = %q, want %q", span.Name(), wantName)
	}
	if span.SpanKind() != trace.SpanKindClient {
		t.Errorf("span kind = %v, want Client", span.SpanKind())
	}

	checks := map[string]any{
		"gen_ai.operation.name":      "chat",
		"gen_ai.system":              mock.ProviderName(),
		"gen_ai.request.model":       mock.ModelID(),
		"gen_ai.usage.input_tokens":  int64(10),
		"gen_ai.usage.output_tokens": int64(20),
	}
	for key, want := range checks {
		v, ok := attrValue(span, key)
		if !ok {
			t.Errorf("missing attribute %q", key)
			continue
		}
		switch w := want.(type) {
		case string:
			if v.AsString() != w {
				t.Errorf("attribute %q = %q, want %q", key, v.AsString(), w)
			}
		case int64:
			if v.AsInt64() != w {
				t.Errorf("attribute %q = %d, want %d", key, v.AsInt64(), w)
			}
		}
	}

	if fr, ok := attrValue(span, "gen_ai.response.finish_reasons"); !ok {
		t.Errorf("missing gen_ai.response.finish_reasons")
	} else {
		got := fr.AsStringSlice()
		if len(got) != 1 || got[0] != string(provider.FinishStop) {
			t.Errorf("finish_reasons = %v, want [%q]", got, provider.FinishStop)
		}
	}

	if span.Status().Code != codes.Ok {
		t.Errorf("status = %v, want Ok", span.Status().Code)
	}
}

func TestGenerate_Error(t *testing.T) {
	tp, sr := newTracerProvider()
	bridge := otelbridge.New(otelbridge.WithTracer(tp.Tracer("test")))

	wantErr := errors.New("boom")
	mock := &aitest.MockModel{Err: wantErr}
	model := ai.TelemetryMiddleware(mock, bridge)

	_, err := model.Generate(context.Background(), provider.Call{})
	if err == nil {
		t.Fatal("expected error")
	}

	ended := sr.Ended()
	if len(ended) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(ended))
	}
	span := ended[0]

	if span.Status().Code != codes.Error {
		t.Errorf("status = %v, want Error", span.Status().Code)
	}
	if span.Status().Description != wantErr.Error() {
		t.Errorf("status description = %q, want %q", span.Status().Description, wantErr.Error())
	}

	events := span.Events()
	found := false
	for _, ev := range events {
		if ev.Name == "exception" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a recorded exception event, got events: %+v", events)
	}
}

func TestStream_EndsOnFinishPart(t *testing.T) {
	tp, sr := newTracerProvider()
	bridge := otelbridge.New(otelbridge.WithTracer(tp.Tracer("test")))

	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{
			{
				provider.TextDelta{Text: "hello"},
				provider.FinishPart{
					Reason: provider.FinishStop,
					Usage:  provider.Usage{InputTokens: 3, OutputTokens: 4},
				},
			},
		},
	}
	model := ai.TelemetryMiddleware(mock, bridge)

	stream, err := model.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Parts() {
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ended := sr.Ended()
	if len(ended) != 1 {
		t.Fatalf("got %d ended spans, want 1", len(ended))
	}
	span := ended[0]

	if v, ok := attrValue(span, "gen_ai.usage.input_tokens"); !ok || v.AsInt64() != 3 {
		t.Errorf("gen_ai.usage.input_tokens = %v, ok=%v, want 3", v, ok)
	}
	if v, ok := attrValue(span, "gen_ai.usage.output_tokens"); !ok || v.AsInt64() != 4 {
		t.Errorf("gen_ai.usage.output_tokens = %v, ok=%v, want 4", v, ok)
	}
	if span.Status().Code != codes.Ok {
		t.Errorf("status = %v, want Ok", span.Status().Code)
	}
}

func TestParenting(t *testing.T) {
	tp, sr := newTracerProvider()
	tracer := tp.Tracer("test")
	bridge := otelbridge.New(otelbridge.WithTracer(tracer))

	mock := &aitest.MockModel{
		Responses: []*provider.Response{{FinishReason: provider.FinishStop}},
	}
	model := ai.TelemetryMiddleware(mock, bridge)

	ctx, parentSpan := tracer.Start(context.Background(), "parent")
	_, err := model.Generate(ctx, provider.Call{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parentSpan.End()

	ended := sr.Ended()
	var child sdktrace.ReadOnlySpan
	for _, s := range ended {
		if s.Name() != "parent" {
			child = s
		}
	}
	if child == nil {
		t.Fatal("child span not found")
	}
	if child.Parent().SpanID() != parentSpan.SpanContext().SpanID() {
		t.Errorf("child parent span id = %v, want %v", child.Parent().SpanID(), parentSpan.SpanContext().SpanID())
	}
}

func TestConcurrent_DistinctSpans(t *testing.T) {
	tp, sr := newTracerProvider()
	bridge := otelbridge.New(otelbridge.WithTracer(tp.Tracer("test")))

	// Each goroutine gets its own MockModel (and its own
	// TelemetryMiddleware wrapper) sharing the single Bridge, so the
	// concurrency under test is Bridge's OnSpanStart/OnSpanEnd map access
	// — not aitest.MockModel's call bookkeeping, which isn't itself meant
	// to be driven concurrently by a single instance.
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			mock := &aitest.MockModel{
				Responses: []*provider.Response{{
					FinishReason: provider.FinishStop,
					Usage:        provider.Usage{InputTokens: i, OutputTokens: i * 2},
				}},
			}
			model := ai.TelemetryMiddleware(mock, bridge)
			_, err := model.Generate(context.Background(), provider.Call{})
			if err != nil {
				t.Errorf("Generate: %v", err)
			}
		}()
	}
	wg.Wait()

	ended := sr.Ended()
	if len(ended) != n {
		t.Fatalf("got %d ended spans, want %d", len(ended), n)
	}

	seen := make(map[string]bool)
	for _, span := range ended {
		id := span.SpanContext().SpanID().String()
		if seen[id] {
			t.Errorf("duplicate span id %s", id)
		}
		seen[id] = true

		in, ok := attrValue(span, "gen_ai.usage.input_tokens")
		if !ok {
			t.Errorf("missing gen_ai.usage.input_tokens")
			continue
		}
		out, ok := attrValue(span, "gen_ai.usage.output_tokens")
		if !ok {
			t.Errorf("missing gen_ai.usage.output_tokens")
			continue
		}
		if out.AsInt64() != in.AsInt64()*2 {
			t.Errorf("attribute isolation broken: input=%d output=%d, want output=2*input", in.AsInt64(), out.AsInt64())
		}
	}
}
