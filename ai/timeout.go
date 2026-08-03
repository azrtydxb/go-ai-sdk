package ai

import (
	"context"
	"errors"
	"time"
)

// Sentinel causes installed on the context.WithTimeoutCause-derived contexts
// Timeout produces, one per dimension. timeoutErrorFor checks
// context.Cause against these to tell "OUR bound fired" apart from any other
// reason a context can be done (in particular, the caller's own ctx being
// canceled or exceeding its own deadline) — see Timeout's doc.
var (
	errTotalTimeout = errors.New("ai: total timeout exceeded")
	errStepTimeout  = errors.New("ai: step timeout exceeded")
	errChunkTimeout = errors.New("ai: chunk timeout exceeded (stream stalled)")
)

// withTotalTimeout derives ctx with Timeout.Total's bound, if t is non-nil
// and Total > 0, using errTotalTimeout as the cancellation cause. Returns ctx
// unchanged with a no-op cancel otherwise.
func withTotalTimeout(ctx context.Context, t *Timeout) (context.Context, context.CancelFunc) {
	if t == nil || t.Total <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, t.Total, errTotalTimeout)
}

// withStepTimeout derives ctx with Timeout.Step's bound, if t is non-nil and
// Step > 0, using errStepTimeout as the cancellation cause. Returns ctx
// unchanged with a no-op cancel otherwise.
func withStepTimeout(ctx context.Context, t *Timeout) (context.Context, context.CancelFunc) {
	if t == nil || t.Step <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, t.Step, errStepTimeout)
}

// timeoutErrorFor reports whether ctx is done BECAUSE one of Timeout's own
// bounds fired — i.e. context.Cause(ctx) is one of this package's sentinel
// causes — as opposed to any other reason (including the caller's own ctx
// being canceled or reaching its own deadline). It returns (nil, false) when
// ctx is not done at all, when t is nil, or when ctx is done for a reason
// that isn't one of ours (the caller-abort case).
//
// Because a context derived from a canceled parent propagates the PARENT's
// cause (canceling a context never affects its parent, but a parent's
// cancellation cause DOES propagate down to every context derived from it),
// checking context.Cause on the deepest context actually used for a call
// correctly reflects whichever bound — Total, Step, or Chunk — fired first,
// with no extra bookkeeping needed to figure out precedence.
func timeoutErrorFor(ctx context.Context, t *Timeout) (*TimeoutError, bool) {
	if t == nil || ctx.Err() == nil {
		return nil, false
	}
	switch context.Cause(ctx) {
	case errTotalTimeout:
		return &TimeoutError{Dimension: "total", Limit: t.Total}, true
	case errStepTimeout:
		return &TimeoutError{Dimension: "step", Limit: t.Step}, true
	case errChunkTimeout:
		return &TimeoutError{Dimension: "chunk", Limit: t.Chunk}, true
	default:
		return nil, false
	}
}

// chunkWatchdog cancels a derived context (with errChunkTimeout as cause)
// if Reset is not called at least once every d. Stop must be called exactly
// once, when the step it was created for ends (success, error, or abandoned
// iteration) — it is nil-receiver-safe so callers don't need to guard every
// call site with a nil check when Timeout.Chunk (and thus the watchdog
// itself) is unset. Stop is idempotent-safe with respect to timer leakage:
// whether or not the watchdog already fired, calling Stop guarantees the
// timer.AfterFunc goroutine either never runs (Stop won the race) or has
// already returned (it ran to completion and exited) — never left pending.
type chunkWatchdog struct {
	timer  *time.Timer
	d      time.Duration
	cancel context.CancelCauseFunc
}

// newChunkWatchdog derives a child of parent that the returned *chunkWatchdog
// cancels (cause errChunkTimeout) if d elapses between calls to Reset. When
// d <= 0, it returns parent unchanged and a nil watchdog (Reset/Stop on a nil
// *chunkWatchdog are no-ops).
func newChunkWatchdog(parent context.Context, d time.Duration) (context.Context, *chunkWatchdog) {
	if d <= 0 {
		return parent, nil
	}
	ctx, cancel := context.WithCancelCause(parent)
	w := &chunkWatchdog{d: d, cancel: cancel}
	w.timer = time.AfterFunc(d, func() { cancel(errChunkTimeout) })
	return ctx, w
}

// Reset restarts the watchdog's timer — call once per yielded stream part.
func (w *chunkWatchdog) Reset() {
	if w == nil {
		return
	}
	w.timer.Reset(w.d)
}

// Stop stops the watchdog's timer (preventing a pending fire) and releases
// its derived context via its own cancel cause. Safe to call even if the
// watchdog already fired.
func (w *chunkWatchdog) Stop() {
	if w == nil {
		return
	}
	w.timer.Stop()
	w.cancel(nil)
}
