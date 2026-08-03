// Package otel bridges the ai package's Telemetry interface to
// OpenTelemetry, emitting spans that follow the OpenTelemetry GenAI
// semantic conventions.
//
// This package lives in its own Go module (github.com/azrtydxb/go-ai-sdk/
// contrib/otel) so that the root go-ai-sdk module stays free of any
// third-party dependency: importing contrib/otel pulls in
// go.opentelemetry.io/otel and go.opentelemetry.io/otel/trace, but the
// root ai/provider packages remain zero-dependency regardless of whether
// any consumer uses this bridge.
//
// Typical usage:
//
//	model = ai.TelemetryMiddleware(baseModel, otel.New())
//
// See README.md for the full list of GenAI-semconv attributes emitted.
package otel
