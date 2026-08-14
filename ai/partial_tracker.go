package ai

// Partial-output tracking. StreamObject (ObjectStream.Partials) and
// StreamText (GenerateTextOpts.OnPartialOutput) both watch a growing raw JSON
// document and report a new snapshot whenever it becomes decodable into a
// value distinct from the last one reported. partialTracker is that single
// implementation, used by both.
//
// Design note — cost per delta. The approach is inherently O(n) in the
// accumulated length for every delta: partialjson.Repair rescans the whole
// document each time, because a repair-based partial parser has no
// incremental state to resume from (a single new byte can change how the
// tail of the document must be closed). The tracker does that scan exactly
// ONCE per delta — it hands the repaired string straight to the mode's
// decodeRepaired rather than letting the decoder repair a second time — and
// removes the redundant work layered on top of it: when a delta leaves the
// REPAIRED document byte-identical (a whitespace-only delta, an empty delta,
// or a provider-assembled ToolCallEnd that merely restates the args already
// streamed), the tracker short-circuits before the json.Unmarshal and the
// reflect.DeepEqual, which are the expensive steps for large documents. The
// remaining per-delta scan is inherent to the repair approach and is only
// removable by replacing it with a genuinely incremental parser.

import (
	"reflect"
	"strings"
)

// partialTracker accumulates a streaming raw JSON document and decides which
// snapshots of it are worth reporting. The zero value is ready to use; it is
// not safe for concurrent use (both callers drive it from a single stream
// goroutine).
type partialTracker struct {
	buf strings.Builder

	// repaired is the partialjson.Repair output for the accumulation as of
	// the last time it was computed, and haveRepaired whether one has been
	// computed at all — the short-circuit key (see the design note above).
	repaired     string
	haveRepaired bool

	// prev is the last value reported to the caller, and havePrev whether
	// anything has been reported — used to suppress repeat snapshots that
	// decode differently but compare equal.
	prev     any
	havePrev bool
}

// feed appends delta to the accumulation and reports the resulting snapshot,
// returning ok=true only for a NEW distinct partial value.
//
// decode is the caller's mode-specific decoder (Output.decodeRepaired, or the
// equivalent closure in ObjectStream.Partials). It receives the string the
// tracker has ALREADY fence-stripped and repaired, and only unmarshals it —
// the repair happens exactly once per delta, here, and is shared between the
// short-circuit and the decode. (A repair is not cheap enough to do twice:
// on a 30KB document it costs roughly three quarters of what the unmarshal
// costs.) decode is called at most once per feed, and not at all when the
// delta left the repaired document unchanged.
func (t *partialTracker) feed(delta []byte, decode func(string) (any, bool)) (any, bool) {
	t.buf.Write(delta)
	return t.emit(decode)
}

// replace discards the accumulation and substitutes raw, then reports like
// feed does. It exists for the provider-assembled ToolCallEnd, whose Args
// carry the authoritative arguments and supersede (rather than extend) the
// deltas that preceded them — appending would double them up.
func (t *partialTracker) replace(raw []byte, decode func(string) (any, bool)) (any, bool) {
	t.buf.Reset()
	t.buf.Write(raw)
	return t.emit(decode)
}

// reset returns the tracker to its zero state, dropping both the
// accumulation and the dedupe history. StreamText resets at every step (the
// decoded output is the LAST step's, so earlier steps' text must not bleed
// into it).
func (t *partialTracker) reset() {
	t.buf.Reset()
	t.repaired = ""
	t.haveRepaired = false
	t.prev = nil
	t.havePrev = false
}

// text returns the raw accumulation verbatim — fences, whitespace, and all.
func (t *partialTracker) text() string { return t.buf.String() }

// emit runs the repair short-circuit, then the decode and the DeepEqual
// dedupe, returning the value to report (if any).
func (t *partialTracker) emit(decode func(string) (any, bool)) (any, bool) {
	repaired, ok := repairPartial(t.buf.String())
	if !ok {
		// Nothing decodable yet (empty, or not the start of a JSON value):
		// leave the short-circuit key untouched so the first repairable
		// document is not mistaken for a repeat.
		return nil, false
	}
	if t.haveRepaired && repaired == t.repaired {
		return nil, false
	}
	t.haveRepaired = true
	t.repaired = repaired

	v, ok := decode(repaired)
	if !ok {
		return nil, false
	}
	if t.havePrev && reflect.DeepEqual(t.prev, v) {
		return nil, false
	}
	t.havePrev = true
	t.prev = v
	return v, true
}

// stripPartialFences removes a markdown code fence from s, which may be a
// PREFIX of a still-streaming document: unlike stripFences (which strips only
// when both ends are fenced, and so never matches mid-stream) it strips a
// leading "```" / "```json" line as soon as it has arrived, and a trailing
// "```" only if one has arrived too. Input that doesn't start with a fence is
// returned unchanged, so this is a no-op for the overwhelmingly common
// unfenced stream.
//
// It mirrors stripFences' notion of a fence — a "```", an optional exact,
// case-sensitive "json" tag, and surrounding whitespace trimmed — so the
// partials a fenced stream reports stay consistent with the final value
// stripFences produces for the same text.
func stripPartialFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimPrefix(t, "json")
	t = strings.TrimSuffix(strings.TrimSpace(t), "```")
	return strings.TrimSpace(t)
}
