package ai

import (
	"iter"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// seqOf returns an iter.Seq[provider.StreamPart] that replays parts in
// order, once.
func seqOf(parts ...provider.StreamPart) iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for _, p := range parts {
			if !yield(p) {
				return
			}
		}
	}
}

// collectText concatenates the Text of every TextDelta in parts, and
// separately returns the raw chunk strings in emission order (useful for
// asserting chunk boundaries, not just the concatenated result).
func collectText(parts []provider.StreamPart) (concatenated string, chunks []string) {
	for _, p := range parts {
		if td, ok := p.(provider.TextDelta); ok {
			concatenated += td.Text
			chunks = append(chunks, td.Text)
		}
	}
	return concatenated, chunks
}

func drain(seq iter.Seq[provider.StreamPart]) []provider.StreamPart {
	var out []provider.StreamPart
	for p := range seq {
		out = append(out, p)
	}
	return out
}

func TestSmoothStreamWordChunkingSingleDelta(t *testing.T) {
	in := seqOf(
		provider.TextDelta{Text: "hello world foo"},
		provider.FinishPart{Reason: provider.FinishStop},
	)
	out := drain(SmoothStream(in, SmoothOpts{}))

	concatenated, chunks := collectText(out)
	if concatenated != "hello world foo" {
		t.Fatalf("concatenated = %q", concatenated)
	}
	want := []string{"hello ", "world ", "foo"}
	if len(chunks) != len(want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Errorf("chunks[%d] = %q, want %q", i, chunks[i], want[i])
		}
	}

	// FinishPart must pass through after the trailing "foo" flush.
	if _, ok := out[len(out)-1].(provider.FinishPart); !ok {
		t.Fatalf("last part = %#v, want FinishPart", out[len(out)-1])
	}
}

// TestSmoothStreamWordChunkingSplitAcrossDeltas covers a word split mid-way
// across two TextDeltas ("wor" + "ld") — it must coalesce into a single
// "world " chunk rather than emitting "wor" and "ld" separately.
func TestSmoothStreamWordChunkingSplitAcrossDeltas(t *testing.T) {
	in := seqOf(
		provider.TextDelta{Text: "hello wor"},
		provider.TextDelta{Text: "ld and mo"},
		provider.TextDelta{Text: "re"},
	)
	out := drain(SmoothStream(in, SmoothOpts{}))

	concatenated, chunks := collectText(out)
	if concatenated != "hello world and more" {
		t.Fatalf("concatenated = %q", concatenated)
	}
	want := []string{"hello ", "world ", "and ", "more"}
	if len(chunks) != len(want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Errorf("chunks[%d] = %q, want %q", i, chunks[i], want[i])
		}
	}
}

// TestSmoothStreamWordChunkingMultiWordDelta covers a single TextDelta
// carrying several complete words at once: each word must still be split
// into its own chunk, not passed through as one big delta.
func TestSmoothStreamWordChunkingMultiWordDelta(t *testing.T) {
	in := seqOf(provider.TextDelta{Text: "one two three "})
	out := drain(SmoothStream(in, SmoothOpts{}))

	_, chunks := collectText(out)
	want := []string{"one ", "two ", "three "}
	if len(chunks) != len(want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Errorf("chunks[%d] = %q, want %q", i, chunks[i], want[i])
		}
	}
}

func TestSmoothStreamLineChunking(t *testing.T) {
	in := seqOf(
		provider.TextDelta{Text: "first line\nsecond "},
		provider.TextDelta{Text: "line\nthird partial"},
	)
	out := drain(SmoothStream(in, SmoothOpts{Chunking: "line"}))

	concatenated, chunks := collectText(out)
	if concatenated != "first line\nsecond line\nthird partial" {
		t.Fatalf("concatenated = %q", concatenated)
	}
	want := []string{"first line\n", "second line\n", "third partial"}
	if len(chunks) != len(want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
	for i := range want {
		if chunks[i] != want[i] {
			t.Errorf("chunks[%d] = %q, want %q", i, chunks[i], want[i])
		}
	}
}

// TestSmoothStreamNonTextFlushesBufferedTextFirst covers a ToolCallDelta
// arriving mid-word: the partially buffered text must flush as a TextDelta
// BEFORE the ToolCallDelta is yielded, preserving emission order.
func TestSmoothStreamNonTextFlushesBufferedTextFirst(t *testing.T) {
	in := seqOf(
		provider.TextDelta{Text: "hello "},
		provider.ToolCallDelta{ID: "1", Name: "get_weather", ArgsDelta: "{}"},
		provider.TextDelta{Text: "world"},
	)
	out := drain(SmoothStream(in, SmoothOpts{}))

	if len(out) != 3 {
		t.Fatalf("out = %#v, want 3 parts", out)
	}
	td0, ok := out[0].(provider.TextDelta)
	if !ok || td0.Text != "hello " {
		t.Fatalf("out[0] = %#v, want TextDelta{hello }", out[0])
	}
	tcd, ok := out[1].(provider.ToolCallDelta)
	if !ok || tcd.ID != "1" {
		t.Fatalf("out[1] = %#v, want the passed-through ToolCallDelta", out[1])
	}
	// "world" has no trailing whitespace and the stream ends right after,
	// so it must flush as the final trailing chunk.
	td1, ok := out[2].(provider.TextDelta)
	if !ok || td1.Text != "world" {
		t.Fatalf("out[2] = %#v, want TextDelta{world}", out[2])
	}
}

// TestSmoothStreamReasoningDeltaPassesThroughUntouched covers the
// documented divergence: ReasoningDelta is free-form text like TextDelta,
// but must NOT be re-chunked — it passes through exactly as received,
// still flushing any buffered TextDelta text first.
func TestSmoothStreamReasoningDeltaPassesThroughUntouched(t *testing.T) {
	in := seqOf(
		provider.TextDelta{Text: "hello "},
		provider.ReasoningDelta{Text: "thinking about many different words here"},
		provider.TextDelta{Text: "world"},
	)
	out := drain(SmoothStream(in, SmoothOpts{}))

	var reasoningSeen []provider.ReasoningDelta
	for _, p := range out {
		if rd, ok := p.(provider.ReasoningDelta); ok {
			reasoningSeen = append(reasoningSeen, rd)
		}
	}
	if len(reasoningSeen) != 1 || reasoningSeen[0].Text != "thinking about many different words here" {
		t.Fatalf("ReasoningDelta parts = %#v, want exactly one untouched", reasoningSeen)
	}
}

// TestSmoothStreamTrailingTextFlushesAtEnd covers text with no trailing
// delimiter at all (word or line): it must still flush once the inner
// sequence ends, rather than being silently dropped.
func TestSmoothStreamTrailingTextFlushesAtEnd(t *testing.T) {
	in := seqOf(provider.TextDelta{Text: "no trailing space"})
	out := drain(SmoothStream(in, SmoothOpts{}))

	concatenated, _ := collectText(out)
	if concatenated != "no trailing space" {
		t.Fatalf("concatenated = %q, want %q", concatenated, "no trailing space")
	}
	last, ok := out[len(out)-1].(provider.TextDelta)
	if !ok || last.Text != "space" {
		t.Fatalf("last part = %#v, want trailing flush TextDelta{space}", out[len(out)-1])
	}
}

// TestSmoothStreamEmptySequenceYieldsNothing covers an inner sequence with
// no parts at all: SmoothStream must yield nothing (not even an empty
// flush).
func TestSmoothStreamEmptySequenceYieldsNothing(t *testing.T) {
	out := drain(SmoothStream(seqOf(), SmoothOpts{}))
	if len(out) != 0 {
		t.Fatalf("out = %#v, want empty", out)
	}
}

// TestSmoothStreamAbandonedEarlyStopsIteratingInner covers a consumer that
// stops ranging early (e.g. a `break`): SmoothStream's inner range loop
// must also stop rather than continuing to pull from parts.
func TestSmoothStreamAbandonedEarlyStopsIteratingInner(t *testing.T) {
	pulled := 0
	in := func(yield func(provider.StreamPart) bool) {
		texts := []string{"one ", "two ", "three "}
		for _, txt := range texts {
			pulled++
			if !yield(provider.TextDelta{Text: txt}) {
				return
			}
		}
	}

	count := 0
	for range SmoothStream(in, SmoothOpts{}) {
		count++
		if count == 1 {
			break
		}
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if pulled != 1 {
		t.Fatalf("pulled = %d, want 1 (inner sequence must stop being pulled once consumer stops)", pulled)
	}
}

func TestSmoothOptsDelayZeroMeansNoSleep(t *testing.T) {
	// Regression guard for the documented divergence from Vercel's
	// smoothStream (10ms implicit default): Delay: 0 (the zero value) must
	// not sleep at all. This test would take >0s to complete if a hidden
	// default delay were applied across the many chunks below.
	var text string
	for i := 0; i < 200; i++ {
		text += "word "
	}
	in := seqOf(provider.TextDelta{Text: text})
	out := drain(SmoothStream(in, SmoothOpts{Delay: 0}))
	if len(out) != 200 {
		t.Fatalf("got %d chunks, want 200", len(out))
	}
}
