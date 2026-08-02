package ai

import (
	"iter"
	"time"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// SmoothOpts configures SmoothStream.
type SmoothOpts struct {
	// Chunking selects how TextDeltas are re-chunked: "word" (default,
	// used when empty) or "line".
	Chunking string
	// Delay is slept after every part SmoothStream emits (both re-chunked
	// text deltas and passed-through parts). Zero means no delay at all —
	// unlike Vercel AI SDK's smoothStream, which defaults to a 10ms delay,
	// this implementation applies NO implicit default: callers that want a
	// delay must set it explicitly. This divergence keeps behavior
	// predictable and keeps tests (which use Delay: 0) fast and
	// deterministic.
	Delay time.Duration
}

// SmoothStream wraps parts, re-chunking its TextDeltas into smaller,
// more evenly sized deltas — "word" mode splits on whitespace boundaries,
// "line" mode splits on newlines — and sleeping Opts.Delay between each
// part it emits. This is purely a presentation aid for driving a UI at a
// steady cadence; it does not change the total text content, only how it
// is broken into deltas over time.
//
// Chunk shape: each emitted chunk is the content unit PLUS its trailing
// delimiter, e.g. word mode emits "hello " (word + trailing whitespace,
// not "hello" then " " separately) and line mode emits "first line\n"
// (line + trailing newline). A word or line split across multiple input
// TextDeltas is buffered and coalesced: nothing is emitted for a partial
// word/line until its trailing delimiter (or the stream's end) arrives.
//
// Only TextDelta parts are re-chunked. Every other StreamPart — including
// ReasoningDelta, which is passed through completely UNTOUCHED (never
// re-chunked, even though it is also free-form text) — flushes any
// currently-buffered text first (as a TextDelta), then is yielded
// unchanged. Any text still buffered when the inner sequence ends is
// flushed as a final TextDelta before SmoothStream itself ends.
func SmoothStream(parts iter.Seq[provider.StreamPart], opts SmoothOpts) iter.Seq[provider.StreamPart] {
	chunking := opts.Chunking
	if chunking == "" {
		chunking = "word"
	}

	return func(yield func(provider.StreamPart) bool) {
		var buf string

		// emit yields s as a TextDelta (no-op for an empty string) and
		// sleeps Delay afterward. Returns false if the consumer stopped
		// iterating.
		emit := func(s string) bool {
			if s == "" {
				return true
			}
			if !yield(provider.TextDelta{Text: s}) {
				return false
			}
			if opts.Delay > 0 {
				time.Sleep(opts.Delay)
			}
			return true
		}

		// flush emits any buffered text as a single TextDelta and clears
		// the buffer. Returns false if the consumer stopped iterating.
		flush := func() bool {
			if buf == "" {
				return true
			}
			s := buf
			buf = ""
			return emit(s)
		}

		for p := range parts {
			td, ok := p.(provider.TextDelta)
			if !ok {
				if !flush() {
					return
				}
				if !yield(p) {
					return
				}
				if opts.Delay > 0 {
					time.Sleep(opts.Delay)
				}
				continue
			}

			buf += td.Text
			var chunks []string
			if chunking == "line" {
				chunks, buf = splitLineChunks(buf)
			} else {
				chunks, buf = splitWordChunks(buf)
			}
			for _, c := range chunks {
				if !emit(c) {
					return
				}
			}
		}

		flush()
	}
}

// splitLineChunks splits buf into complete "line\n" chunks (each including
// its trailing newline) plus a remainder holding any trailing partial line
// (text after the last newline, possibly empty).
func splitLineChunks(buf string) (chunks []string, remainder string) {
	start := 0
	for i := 0; i < len(buf); i++ {
		if buf[i] == '\n' {
			chunks = append(chunks, buf[start:i+1])
			start = i + 1
		}
	}
	return chunks, buf[start:]
}

// splitWordChunks splits buf into complete "word + trailing whitespace"
// chunks, plus a remainder holding any trailing content that isn't yet
// known to be a complete chunk: a word with no whitespace after it yet
// (which may continue in a later delta) is held back rather than emitted,
// so words split across TextDeltas coalesce into one chunk. A leading
// whitespace run with no preceding word in buf (e.g. buf starts with " ")
// is folded into the following word's chunk rather than emitted alone.
func splitWordChunks(buf string) (chunks []string, remainder string) {
	i := 0
	n := len(buf)
	for i < n {
		start := i

		for i < n && isSpace(buf[i]) {
			i++
		}
		wordStart := i
		for i < n && !isSpace(buf[i]) {
			i++
		}
		if wordStart == i {
			// Reached the end of buf while still consuming whitespace
			// (buf[start:] is all whitespace, no word followed): hold it
			// back, it may be a leading run for a word arriving later.
			return chunks, buf[start:]
		}

		wsStart := i
		for i < n && isSpace(buf[i]) {
			i++
		}
		if wsStart == i {
			// Word has no trailing whitespace yet (buf ended mid-word or
			// exactly at the word boundary) — may still be extended by a
			// later delta, so hold the whole thing back.
			return chunks, buf[start:]
		}

		chunks = append(chunks, buf[start:i])
	}
	return chunks, ""
}

func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}
