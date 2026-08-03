# contrib/otel

An OpenTelemetry bridge for [go-ai-sdk](https://github.com/azrtydxb/go-ai-sdk),
implementing `ai.Telemetry` to emit spans following the OpenTelemetry GenAI
semantic conventions.

## Why a separate module

This package lives in its own Go module
(`github.com/azrtydxb/go-ai-sdk/contrib/otel`), distinct from the root
`github.com/azrtydxb/go-ai-sdk` module. That keeps the root module
zero-dependency: importing `contrib/otel` pulls in
`go.opentelemetry.io/otel` and `go.opentelemetry.io/otel/trace`, but only
for consumers that actually want OTel integration. Anyone who doesn't use
this bridge never sees those dependencies show up in their build.

`contrib/otel/go.mod` has a `replace github.com/azrtydxb/go-ai-sdk =>
../..` directive so it builds against the local root module during
development. This replace is dev-only: the root and contrib/otel modules
are tagged together, so published/tagged consumers resolve
`github.com/azrtydxb/go-ai-sdk` to the matching tagged root version rather
than a local path.

## Usage

```go
import (
	"github.com/azrtydxb/go-ai-sdk/ai"
	otelbridge "github.com/azrtydxb/go-ai-sdk/contrib/otel"
)

model = ai.TelemetryMiddleware(baseModel, otelbridge.New())
```

By default, spans are started on `otel.Tracer("github.com/azrtydxb/go-ai-sdk")`.
Use `otelbridge.WithTracer` to supply a different `trace.Tracer`, and
`otelbridge.WithSpanNamePrefix` to prepend a prefix to every span name.

Because `OnSpanStart` receives the call's `context.Context`, spans
automatically parent under any span already present in that context (e.g.
a request-scoped span your application started), via the standard
`trace.Tracer.Start` behavior.

## Span mapping

One `ai.SpanInfo` start/end pair (matched by `CorrelationID`) becomes one
OTel span:

- **Span name**: `<prefix>` + `"chat "` + `ModelID` (both `ai.SpanInfo.Operation`
  values, `"generate"` and `"stream"`, map to the GenAI operation `"chat"`).
- **Kind**: `trace.SpanKindClient`.

### Start attributes

| Attribute | Value |
|---|---|
| `gen_ai.operation.name` | `"chat"` |
| `gen_ai.system` | `SpanInfo.ProviderName` |
| `gen_ai.request.model` | `SpanInfo.ModelID` |

### End attributes / status

| Attribute | Value |
|---|---|
| `gen_ai.usage.input_tokens` | `SpanInfo.Usage.InputTokens` |
| `gen_ai.usage.output_tokens` | `SpanInfo.Usage.OutputTokens` |
| `gen_ai.response.finish_reasons` | `[]string{string(SpanInfo.FinishReason)}`, set only when `FinishReason` is non-empty |

If `SpanInfo.Err != nil`, the bridge calls `span.RecordError(err)` and sets
status `codes.Error` with the error's message; otherwise it sets status
`codes.Ok`. The span is then ended.

These keys are emitted as plain string attributes rather than imported
from a versioned `semconv` package, so this bridge doesn't couple to a
specific OpenTelemetry semconv release.

## Testing

```
cd contrib/otel
go test -race ./...
```

Tests use `go.opentelemetry.io/otel/sdk/trace` with
`tracetest.NewSpanRecorder` to inspect finished spans in-memory — no
network or external collector required.
