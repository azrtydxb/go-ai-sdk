package ai

import "context"

// RuntimeContext is an arbitrary bag of application values made available to
// tools during execution via RuntimeContextFrom. Set GenerateTextOpts.
// RuntimeContext to have it installed on the ctx passed to Tool.Execute (and,
// per RequireApproval, to ApprovalRequired and GenerateTextOpts.
// ApproveToolCall) for the duration of that GenerateText/StreamText call. It
// is installed once, before the tool loop begins — both loops install the
// SAME RuntimeContext value for every step and every resumed batch.
type RuntimeContext map[string]any

// runtimeContextKey is the unexported context key RuntimeContext is stored
// under; unexported so only this package can install or read it, forcing
// callers through RuntimeContextFrom.
type runtimeContextKey struct{}

// withRuntimeContext returns a ctx with rc installed, or ctx unchanged when
// rc is nil (so RuntimeContextFrom keeps reporting nil, per its doc, rather
// than a spuriously-installed nil map).
func withRuntimeContext(ctx context.Context, rc RuntimeContext) context.Context {
	if rc == nil {
		return ctx
	}
	return context.WithValue(ctx, runtimeContextKey{}, rc)
}

// RuntimeContextFrom returns the RuntimeContext installed for this tool
// loop, or nil when none was configured (GenerateTextOpts.RuntimeContext was
// nil/unset, or ctx is unrelated to any GenerateText/StreamText call).
func RuntimeContextFrom(ctx context.Context) RuntimeContext {
	rc, _ := ctx.Value(runtimeContextKey{}).(RuntimeContext)
	return rc
}
