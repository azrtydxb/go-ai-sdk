# Telemetry

`ai.TelemetryMiddleware` wraps a `provider.LanguageModel` so every
`Generate`/`Stream` call reports a span through a small `ai.Telemetry`
interface — the SDK has no built-in dependency on OpenTelemetry or any
other tracing system; you implement `Telemetry` yourself, optionally
bridging to whatever you already use.

## Telemetry and SpanInfo

```go
type Telemetry interface {
	OnSpanStart(info SpanInfo)
	OnSpanEnd(info SpanInfo)
}

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
```

`OnSpanStart` is called with only `Operation`/`ModelID`/`ProviderName`/
`StartTime` populated (`EndTime` zero, `Usage` zero, `FinishReason` empty,
`Err` nil). `OnSpanEnd` is called with `EndTime` always set, and either
`Usage`/`FinishReason` (on success) or `Err` (on failure) — never both.
Implementations must be safe for concurrent use, since a
middleware-wrapped model may be called concurrently.

```go
type logTelemetry struct {
	mu   sync.Mutex
	logs []string
}

func (t *logTelemetry) OnSpanStart(info ai.SpanInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, fmt.Sprintf("start %s %s/%s", info.Operation, info.ProviderName, info.ModelID))
}

func (t *logTelemetry) OnSpanEnd(info ai.SpanInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if info.Err != nil {
		t.logs = append(t.logs, fmt.Sprintf("end %s err=%v", info.Operation, info.Err))
		return
	}
	t.logs = append(t.logs, fmt.Sprintf("end %s reason=%s tokens=%d", info.Operation, info.FinishReason, info.Usage.TotalTokens))
}
```

## TelemetryMiddleware

```go
model := ai.TelemetryMiddleware(baseModel, telemetryImpl)
```

`Generate` emits exactly one span per call, ending when `Generate` returns
(`Usage`/`FinishReason` on success, `Err` on failure). A failure to even
start a stream (`model.Stream` returning a non-nil error) ends the span
immediately with that `Err`, and no `StreamResponse` is ever wrapped or
returned.

### Stream span lifecycle

For `Stream`, the span ends at the **first** of these to happen:

1. The stream's `FinishPart` is observed during `Parts()` iteration — the
   span ends immediately with `Usage`/`FinishReason` taken from it.
2. If no `FinishPart` is ever observed, the span ends once `Parts()`
   iteration ends for any other reason: a mid-stream error
   (`StreamResponse.Err()`, recorded as `Err`), the consumer abandoning
   iteration early, or `Close` being called before either of those
   happens.

In every case the span ends with whatever is known at that point — nothing
is buffered or invented to make an abandoned/errored stream look complete.
Ending is idempotent: whichever of the above happens first is what's
reported; later triggers for the same call are no-ops.

### Single-consumer caveat

Like the rest of the SDK's `StreamResponse` implementations,
`telemetryStream` assumes a single consumer driving `Parts()` to
completion. It does not synchronize its internal ended-flag/span-info
against a concurrent `Close()` call made from another goroutine while
`Parts()` is still being iterated on a different one — calling `Close`
concurrently with an in-progress `Parts()` iteration on the same stream is
a data race, independent of telemetry.

## OTel bridge sketch

`go-ai-sdk` has zero external dependencies, so it can't ship an OTel
integration directly — but `Telemetry` is exactly the shape needed to
bridge to one: start a span in `OnSpanStart`, stash it, end it in
`OnSpanEnd`. The sketch below stands in `otelTracer`/`otelSpan` for a real
`go.opentelemetry.io/otel/trace` tracer/span pair (a real bridge would call
`tracer.Start(ctx, name)` and `span.End()`/`span.SetAttributes(...)`/
`span.RecordError(...)` instead):

```go
type otelBridge struct {
	tracer otelTracer

	mu    sync.Mutex
	spans map[interface{}]*otelSpan // keyed by SpanInfo.StartTime here;
	                                // a real bridge would thread a span
	                                // through ctx or a per-call id instead
}

func (b *otelBridge) OnSpanStart(info ai.SpanInfo) {
	span := b.tracer.Start(info.Operation + " " + info.ModelID)
	b.mu.Lock()
	b.spans[info.StartTime] = span
	b.mu.Unlock()
}

func (b *otelBridge) OnSpanEnd(info ai.SpanInfo) {
	b.mu.Lock()
	span, ok := b.spans[info.StartTime]
	delete(b.spans, info.StartTime)
	b.mu.Unlock()
	if ok {
		span.End()
	}
}

var _ ai.Telemetry = (*otelBridge)(nil)
```

The `StartTime`-keyed map is a simplification for this sketch (two spans
starting in the same nanosecond would collide); a production bridge should
carry the span through `context.Context` — updating `OnSpanStart`'s caller
to make `Generate`/`Stream`'s `ctx` available to it — or key by a
per-call correlation id instead.

## Source of truth

- [`ai/telemetry.go`](../../ai/telemetry.go)

See also: [Middleware and registry](middleware-and-registry.md) for how
`TelemetryMiddleware` composes with the other middlewares;
[Streaming](streaming.md) for the `StreamResponse` iteration contract
`telemetryStream` wraps.
