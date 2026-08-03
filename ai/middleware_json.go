package ai

import (
	"context"
	"iter"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ExtractJSONMiddleware wraps model so that markdown code fences around its
// text output (a common way models wrap JSON they were asked to produce
// "raw", e.g. "```json\n{...}\n```") are stripped before the text reaches
// the caller.
//
// Generate reuses GenerateObject's non-native-JSON decoding rule exactly
// (see stripFences): the response text is trimmed, and a leading "```" (or
// "```json") plus a trailing "```" are removed together — only when BOTH
// are present at their respective ends of the (trimmed) text. Text that
// isn't fenced at both ends passes through unchanged, including text with
// fence lines embedded in the middle of otherwise-unfenced prose.
//
// Stream strips fences incrementally, mirroring that whole-text rule as
// closely as streaming allows:
//
//   - An opening fence line is stripped only when it is the first
//     non-empty line of the stream (mirroring the leading-fence half of
//     stripFences) — this can be decided and emitted immediately, no
//     buffering needed.
//   - A closing fence line is stripped only when it terminates the
//     stream — nothing but whitespace follows it before the stream ends
//     (mirroring the trailing-fence half of stripFences). Since streaming
//     can't know "nothing follows" until the stream actually ends, a
//     candidate closing-fence line (plus any whitespace after it) is
//     buffered rather than emitted immediately; if non-whitespace content
//     later arrives, the buffered candidate is flushed verbatim as
//     ordinary text — it wasn't terminal after all — before scanning
//     resumes from that point.
//   - Every other "```"-prefixed line — one that is neither the first
//     non-empty line nor turns out to terminate the stream — passes
//     through unchanged, same as prose-embedded fences under Generate's
//     rule.
//
// One divergence from Generate's whole-text rule is unavoidable in
// streaming: Generate requires BOTH a leading and a trailing fence before
// stripping either (a leading fence alone is left untouched, since the
// text might not actually be fenced). Stream cannot wait indefinitely to
// find out whether a closing fence will ever arrive, so it strips a
// resolved opening fence unconditionally, even if the stream then ends
// without a matching closing fence — a truncated stream ends up with its
// opening fence stripped and no closing fence to strip (there being none).
//
// A fence marker split across two deltas (e.g. two backticks then
// "`json\n") is still recognized in both directions, since the relevant
// undecided prefix carries over between feeds.
func ExtractJSONMiddleware(model provider.LanguageModel) provider.LanguageModel {
	return &extractJSONModel{model: model}
}

type extractJSONModel struct {
	model provider.LanguageModel
}

func (m *extractJSONModel) ModelID() string      { return m.model.ModelID() }
func (m *extractJSONModel) ProviderName() string { return m.model.ProviderName() }
func (m *extractJSONModel) Capabilities() provider.Capabilities {
	return m.model.Capabilities()
}

func (m *extractJSONModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	resp, err := m.model.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	newContent := make([]provider.ContentPart, len(resp.Content))
	for i, part := range resp.Content {
		if tp, ok := part.(provider.TextPart); ok {
			newContent[i] = provider.TextPart{Text: stripFences(tp.Text)}
			continue
		}
		newContent[i] = part
	}
	return &provider.Response{
		Content:          newContent,
		FinishReason:     resp.FinishReason,
		Usage:            resp.Usage,
		Raw:              resp.Raw,
		ProviderMetadata: resp.ProviderMetadata,
	}, nil
}

func (m *extractJSONModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.model.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	return &extractJSONStream{inner: inner}, nil
}

// extractJSONStream wraps a provider.StreamResponse, running its TextDeltas
// through a fenceScanner and re-emitting whatever it lets through as new
// TextDelta parts (dropping the delta entirely if the scanner produced no
// output for it, e.g. it was wholly a fence line). Non-text parts pass
// through unchanged and reset the scanner's line-start tracking, mirroring
// ExtractReasoningMiddleware's stream boundary handling.
type extractJSONStream struct {
	inner provider.StreamResponse
}

func (s *extractJSONStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		stopped := false
		emit := func(p provider.StreamPart) {
			if stopped {
				return
			}
			if !yield(p) {
				stopped = true
			}
		}

		sc := newFenceScanner(func(text string) {
			if text == "" {
				return
			}
			emit(provider.TextDelta{Text: text})
		})

		for p := range s.inner.Parts() {
			if stopped {
				break
			}
			if td, ok := p.(provider.TextDelta); ok {
				sc.feed([]byte(td.Text))
			} else {
				sc.finish()
				sc = newFenceScanner(sc.emit)
				emit(p)
			}
			if stopped {
				break
			}
		}
		if !stopped {
			sc.finish()
		}
	}
}

func (s *extractJSONStream) Err() error   { return s.inner.Err() }
func (s *extractJSONStream) Close() error { return s.inner.Close() }

// fenceScanner state values.
const (
	fenceLineStart      = iota // deciding whether the current line starts with "```"
	fencePassthrough           // mid-line, known not a fence line: pass bytes through
	fenceOpeningDiscard        // mid the (unconditionally stripped) opening-fence line: discard bytes to end of line
	fenceCandidateLine         // mid a candidate closing-fence line: buffering bytes to end of line, undecided
	fenceCandidateHold         // candidate closing-fence line fully read; buffering subsequent bytes pending resolution
)

// fenceScanner incrementally strips markdown code-fence lines from a byte
// stream, emitting everything else via emit. It mirrors stripFences'
// whole-text semantics (see ExtractJSONMiddleware's doc comment) as closely
// as streaming allows:
//
//   - The first non-empty line of the stream, if it starts with "```", is
//     an opening fence: stripped unconditionally and immediately (no
//     buffering needed — this decision never depends on what follows).
//   - Any later line that starts with "```" is only a CANDIDATE closing
//     fence: it (and any whitespace-only lines/bytes after it) is buffered,
//     not emitted, until either non-whitespace content arrives (the
//     candidate wasn't terminal after all — flush it verbatim as ordinary
//     text, then resume scanning from the triggering byte) or the stream
//     ends with nothing but whitespace having followed the candidate (it
//     WAS the terminal closing fence — discard it, buffered whitespace
//     included).
//
// At most one candidate closing-fence line (plus whatever whitespace
// follows it, until resolved) is ever buffered; everything else streams
// through with no buffering at all.
type fenceScanner struct {
	state int

	lineBuf []byte // up to 3 bytes, used in fenceLineStart to detect "```"

	beforeFirstLine bool // true until the first non-empty line has begun
	lineIsFirst     bool // captured beforeFirstLine at the start of the line currently being buffered in lineBuf

	candidate []byte // buffered candidate closing-fence line (+ trailing bytes); used in fenceCandidateLine/fenceCandidateHold

	emit func(string)
}

func newFenceScanner(emit func(string)) *fenceScanner {
	return &fenceScanner{emit: emit, beforeFirstLine: true}
}

// isFenceWhitespace reports whether b is one of the ASCII whitespace bytes
// that stripFences' surrounding strings.TrimSpace would also treat as
// insignificant around a fence marker.
func isFenceWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\r', '\n', '\v', '\f':
		return true
	}
	return false
}

func (s *fenceScanner) feed(data []byte) {
	for _, b := range data {
		s.step(b)
	}
}

func (s *fenceScanner) step(b byte) {
	switch s.state {
	case fenceLineStart:
		if b == '\n' {
			// A line shorter than 3 bytes can never be a fence marker
			// ("```" requires exactly 3 backticks): flush whatever was
			// buffered plus this newline as ordinary text, and stay at
			// line start for the next line. A wholly empty line (buf still
			// empty) doesn't resolve beforeFirstLine either way.
			if len(s.lineBuf) > 0 {
				s.emit(string(s.lineBuf))
				s.lineBuf = nil
				s.beforeFirstLine = false
			}
			s.emit("\n")
			return
		}

		if len(s.lineBuf) == 0 {
			// This is the first byte of a new line: capture whether THIS
			// line is the stream's first non-empty line before flipping
			// the flag — the flip must happen now, not when the 3-byte
			// fence decision resolves a couple of bytes later, since this
			// line claims the "first non-empty line" slot regardless of
			// whether it turns out to be a fence.
			s.lineIsFirst = s.beforeFirstLine
			s.beforeFirstLine = false
		}

		s.lineBuf = append(s.lineBuf, b)
		if len(s.lineBuf) < 3 {
			return
		}

		if string(s.lineBuf) == "```" {
			s.lineBuf = nil
			if s.lineIsFirst {
				s.state = fenceOpeningDiscard
			} else {
				s.candidate = append(s.candidate, '`', '`', '`')
				s.state = fenceCandidateLine
			}
		} else {
			flushed := string(s.lineBuf)
			s.lineBuf = nil
			s.emit(flushed)
			s.state = fencePassthrough
		}

	case fencePassthrough:
		s.emit(string(b))
		if b == '\n' {
			s.state = fenceLineStart
		}

	case fenceOpeningDiscard:
		if b == '\n' {
			s.state = fenceLineStart
		}
		// Every other byte on the opening-fence line is discarded, whether
		// or not it's part of a "json" suffix.

	case fenceCandidateLine:
		s.candidate = append(s.candidate, b)
		if b == '\n' {
			s.state = fenceCandidateHold
		}

	case fenceCandidateHold:
		if isFenceWhitespace(b) {
			s.candidate = append(s.candidate, b)
			return
		}
		// Non-whitespace content arrived: the buffered candidate was not,
		// after all, the terminal closing fence. Flush it verbatim as
		// ordinary text, then reprocess b fresh from the resulting
		// position — mid-line if the candidate's last byte wasn't a
		// newline, otherwise at a new line's start (so b itself is
		// eligible to begin a fresh candidate).
		flushed := string(s.candidate)
		atLineStart := len(s.candidate) == 0 || s.candidate[len(s.candidate)-1] == '\n'
		s.candidate = nil
		s.emit(flushed)
		if atLineStart {
			s.state = fenceLineStart
		} else {
			s.state = fencePassthrough
		}
		s.step(b)
	}
}

// finish flushes or discards whatever's pending at the end of input:
//
//   - fenceLineStart with a short undecided prefix buffered (fewer than 3
//     bytes, no newline yet) can never complete into a fence line: it is
//     emitted as ordinary text, verbatim.
//   - fenceCandidateLine/fenceCandidateHold means a candidate closing-fence
//     line (plus, in the Hold case, only whitespace) reached the end of the
//     stream with nothing else following it — it IS the terminal closing
//     fence, so it (and any buffered trailing whitespace) is discarded,
//     mirroring stripFences removing the trailing "```" and the whitespace
//     TrimSpace would have absorbed around it.
//   - fencePassthrough/fenceOpeningDiscard have nothing buffered (already
//     emitted or already discarded byte-by-byte) — nothing to do.
func (s *fenceScanner) finish() {
	switch s.state {
	case fenceLineStart:
		if len(s.lineBuf) > 0 {
			s.emit(string(s.lineBuf))
			s.lineBuf = nil
		}
	case fenceCandidateLine, fenceCandidateHold:
		s.candidate = nil
	}
}
