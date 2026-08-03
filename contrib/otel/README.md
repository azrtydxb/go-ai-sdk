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
development. This replace is dev-only and is meant to be dropped (or at
least made irrelevant) once both modules are tagged together: on this
branch, `contrib/otel/go.mod` still requires root
`github.com/azrtydxb/go-ai-sdk v0.1.0`, a version that predates this
wave's `OnSpanStart(ctx, ...)`/`CorrelationID` additions this bridge
depends on. Until `contrib/otel/go.mod`'s require line is bumped to
`v0.2.0` **and** a matching `contrib/otel/v0.2.0` tag is cut — see
[the v6 parity final audit's release procedure](../../docs/superpowers/specs/2026-08-03-v6-parity-final-audit.md#release-procedure-root-v020--contribotel-v020)
for the exact steps and ordering — an external consumer running `go get
.../contrib/otel` resolves root `v0.1.0` and fails to compile. Until that
bump-and-tag lands, external consumers must either add their own
`replace github.com/azrtydxb/go-ai-sdk` directive (pinned to `v0.2.0` or
later, or a local checkout) or depend on a post-bump commit pseudo-version
of `contrib/otel` directly. In short: this module targets root `v0.2.0+`,
but that isn't resolvable through normal tagged-module resolution yet.

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
