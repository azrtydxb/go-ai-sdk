package otel

import (
	"context"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// GenAI semantic-convention attribute keys emitted by Bridge. Declared as
// plain strings (rather than imported from a semconv package version) so
// this bridge doesn't couple to a specific semconv release; consumers can
// rely on these exact key names being stable GenAI semconv attributes.
const (
	attrOperationName = "gen_ai.operation.name"
	attrSystem        = "gen_ai.system"
	attrRequestModel  = "gen_ai.request.model"
	attrUsageInput    = "gen_ai.usage.input_tokens"
	attrUsageOutput   = "gen_ai.usage.output_tokens"
	attrFinishReasons = "gen_ai.response.finish_reasons"
)

// Bridge implements ai.Telemetry by emitting OpenTelemetry GenAI-semantic-
// convention spans on a Tracer. One Bridge can be shared across any number
// of TelemetryMiddleware-wrapped models; it is safe for concurrent use.
type Bridge struct {
	tracer trace.Tracer
	prefix string

	mu    sync.Mutex
	spans map[string]trace.Span
}

// Option configures a Bridge.
type Option func(*Bridge)

// WithTracer sets the trace.Tracer used to start spans. Default:
// otel.Tracer("github.com/azrtydxb/go-ai-sdk").
func WithTracer(t trace.Tracer) Option {
	return func(b *Bridge) { b.tracer = t }
}

// WithSpanNamePrefix sets a prefix prepended to every span name. Default ""
// — the span name is just the gen_ai operation, e.g. "chat gpt-4o".
func WithSpanNamePrefix(prefix string) Option {
	return func(b *Bridge) { b.prefix = prefix }
}

// New returns a Bridge usable as ai.Telemetry:
//
//	model = ai.TelemetryMiddleware(base, otel.New())
func New(opts ...Option) *Bridge {
	b := &Bridge{
		tracer: otel.Tracer("github.com/azrtydxb/go-ai-sdk"),
		spans:  make(map[string]trace.Span),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// OnSpanStart implements ai.Telemetry. It starts a new span (a child of any
// span already present in ctx), records it under info.CorrelationID, and
// sets the GenAI request attributes.
func (b *Bridge) OnSpanStart(ctx context.Context, info ai.SpanInfo) {
	name := b.prefix + "chat " + info.ModelID
	_, span := b.tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String(attrOperationName, "chat"),
			attribute.String(attrSystem, info.ProviderName),
			attribute.String(attrRequestModel, info.ModelID),
		),
	)

	b.mu.Lock()
	b.spans[info.CorrelationID] = span
	b.mu.Unlock()
}

// OnSpanEnd implements ai.Telemetry. It looks up the span started for
// info.CorrelationID, sets the GenAI response/usage attributes and status,
// and ends it. If no span is found (which shouldn't happen), it is ignored
// defensively.
func (b *Bridge) OnSpanEnd(info ai.SpanInfo) {
	b.mu.Lock()
	span, ok := b.spans[info.CorrelationID]
	if ok {
		delete(b.spans, info.CorrelationID)
	}
	b.mu.Unlock()

	if !ok {
		return
	}

	span.SetAttributes(
		attribute.Int(attrUsageInput, info.Usage.InputTokens),
		attribute.Int(attrUsageOutput, info.Usage.OutputTokens),
	)
	if info.FinishReason != "" {
		span.SetAttributes(attribute.StringSlice(attrFinishReasons, []string{string(info.FinishReason)}))
	}

	if info.Err != nil {
		span.RecordError(info.Err)
		span.SetStatus(codes.Error, info.Err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}

	span.End()
}
