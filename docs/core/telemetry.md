# Telemetry

`ai.TelemetryMiddleware` wraps a `provider.LanguageModel` so every
`Generate`/`Stream` call reports a span through a small `ai.Telemetry`
interface — the root SDK has no built-in dependency on OpenTelemetry or any
other tracing system. Implement `Telemetry` yourself for a lightweight
non-OTel need (logging, a custom metrics sink), or use the real
[`contrib/otel`](#the-contribotel-bridge) bridge, shipped as a separate Go
module, to get OpenTelemetry GenAI-semantic-convention spans with zero
lines of bridging code.

## Telemetry and SpanInfo

```go
type Telemetry interface {
	OnSpanStart(ctx context.Context, info SpanInfo)
	OnSpanEnd(info SpanInfo)
}

type SpanInfo struct {
	CorrelationID string // stable id shared by the start/end pair for one call
	Operation     string // "generate" | "stream"
	ModelID       string
	ProviderName  string
	StartTime     time.Time
	EndTime       time.Time      // zero on Start
	Usage         provider.Usage // zero on Start
	FinishReason  provider.FinishReason
	Err           error
}
```

**Breaking change (v0.2.0):** `OnSpanStart` now takes `ctx context.Context`
as its first argument, and `SpanInfo` gains `CorrelationID`. Migrating an
existing `Telemetry` implementation is a one-line signature change:

```go
// Before (v0.1.x)
func (t *myTelemetry) OnSpanStart(info ai.SpanInfo) { ... }

// After (v0.2.0)
func (t *myTelemetry) OnSpanStart(ctx context.Context, info ai.SpanInfo) { ... }
```

`ctx` is the underlying model call's own context — read a parent span out
of it (e.g. via OTel's `trace.SpanFromContext`) to attach a new span as its
child; the SDK doesn't use anything `OnSpanStart` derives or returns from
`ctx`, since the provider call is a leaf. `CorrelationID` is a
process-wide, monotonically increasing string (an atomic counter, not
time-based) that's identical on the `OnSpanStart` and `OnSpanEnd` (or
stream-end) `SpanInfo` for one call — key a span map by it instead of by
`StartTime`, which can collide under concurrent or fast-successive calls.

`OnSpanStart` is called with `CorrelationID`/`Operation`/`ModelID`/
`ProviderName`/`StartTime` populated (`EndTime` zero, `Usage` zero,
`FinishReason` empty, `Err` nil). `OnSpanEnd` is called with the same
`CorrelationID`, `EndTime` always set, and either `Usage`/`FinishReason`
(on success) or `Err` (on failure) — never both. Implementations must be
safe for concurrent use, since a middleware-wrapped model may be called
concurrently.

```go
type logTelemetry struct {
	mu   sync.Mutex
	logs []string
}

func (t *logTelemetry) OnSpanStart(ctx context.Context, info ai.SpanInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.logs = append(t.logs, fmt.Sprintf("start[%s] %s %s/%s", info.CorrelationID, info.Operation, info.ProviderName, info.ModelID))
}

func (t *logTelemetry) OnSpanEnd(info ai.SpanInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if info.Err != nil {
		t.logs = append(t.logs, fmt.Sprintf("end[%s] %s err=%v", info.CorrelationID, info.Operation, info.Err))
		return
	}
	t.logs = append(t.logs, fmt.Sprintf("end[%s] %s reason=%s tokens=%d", info.CorrelationID, info.Operation, info.FinishReason, info.Usage.TotalTokens))
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

## The contrib/otel bridge

[`contrib/otel`](../../contrib/otel/README.md) is a real, ready-to-use
OpenTelemetry bridge implementing `ai.Telemetry` — no sketch, no bridging
code to write yourself. It's a **separate Go module**
(`github.com/azrtydxb/go-ai-sdk/contrib/otel`), not part of the root
module, specifically so that importing it (and its
`go.opentelemetry.io/otel`/`.../trace` dependencies) is entirely opt-in:
the root `go-ai-sdk` module stays zero-dependency for everyone who doesn't
need OTel.

```go
import (
	"github.com/azrtydxb/go-ai-sdk/ai"
	otelbridge "github.com/azrtydxb/go-ai-sdk/contrib/otel"
)

model := ai.TelemetryMiddleware(baseModel, otelbridge.New())
```

`otelbridge.New(...Option)` returns a `*Bridge` implementing `ai.Telemetry`
by starting a `trace.SpanKindClient` span per call (`prefix + "chat " +
ModelID` as the span name — both `SpanInfo.Operation` values, `"generate"`
and `"stream"`, map to the same GenAI operation, `"chat"`), keyed
internally by `SpanInfo.CorrelationID` so the start/end pair for one call
is matched reliably even under concurrent calls. Because `OnSpanStart`
receives the call's `ctx`, a span started here automatically parents under
any span already present in that `ctx` (e.g. a request-scoped span your
own application code started), via `trace.Tracer.Start`'s normal
ctx-based parenting — no extra wiring needed.

`otelbridge.WithTracer(t trace.Tracer)` supplies a non-default tracer
(default: `otel.Tracer("github.com/azrtydxb/go-ai-sdk")`);
`otelbridge.WithSpanNamePrefix(prefix string)` prepends `prefix` to every
span name.

### Span attributes

One `SpanInfo` start/end pair becomes one span with the following
[GenAI semantic-convention](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
attributes (emitted as plain string keys, not from an imported `semconv`
package version, so the bridge doesn't couple to a specific semconv
release):

| Attribute | Set at | Value |
|---|---|---|
| `gen_ai.operation.name` | Start | `"chat"` |
| `gen_ai.system` | Start | `SpanInfo.ProviderName` |
| `gen_ai.request.model` | Start | `SpanInfo.ModelID` |
| `gen_ai.usage.input_tokens` | End | `SpanInfo.Usage.InputTokens` |
| `gen_ai.usage.output_tokens` | End | `SpanInfo.Usage.OutputTokens` |
| `gen_ai.response.finish_reasons` | End | `[]string{string(SpanInfo.FinishReason)}`, set only when `FinishReason` is non-empty |

If `SpanInfo.Err != nil`, the bridge calls `span.RecordError(err)` and sets
status `codes.Error` with the error's message; otherwise it sets status
`codes.Ok`. The span then ends. This all rides on the exact
`TelemetryMiddleware` lifecycle described above — the same "span ends at
the first of FinishPart / mid-stream error / abandonment / Close" rule
governs when a streamed call's OTel span ends, since the bridge itself
never sees anything but `OnSpanStart`/`OnSpanEnd` calls.

### Testing contrib/otel

`contrib/otel` is a separate module: the root `go test ./...` does **not**
build or run its tests. Verify it from inside the module:

```sh
cd contrib/otel
go build ./... && go vet ./... && go test -race ./...
```

Full details — the module's `replace` directive (dev-only; tagged
consumers resolve the root module by its tagged version, not the local
path), attribute-mapping table, and test setup (an in-memory
`tracetest.SpanRecorder`, no network/collector required) — are in
[`contrib/otel/README.md`](../../contrib/otel/README.md).

## Source of truth

- [`ai/telemetry.go`](../../ai/telemetry.go)
- [`contrib/otel/otel.go`](../../contrib/otel/otel.go),
  [`contrib/otel/README.md`](../../contrib/otel/README.md)

See also: [Middleware and registry](middleware-and-registry.md) for how
`TelemetryMiddleware` composes with the other middlewares;
[Streaming](streaming.md) for the `StreamResponse` iteration contract
`telemetryStream` wraps; [Architecture § Observability](../architecture.md#observability-and-the-nested-contribotel-module)
for why `contrib/otel` lives outside the root module.
