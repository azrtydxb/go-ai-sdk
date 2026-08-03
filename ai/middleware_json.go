package ai

import (
	"bytes"
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
// closely as streaming allows — and, unlike Generate, with no notion of
// "lines": a fence marker is recognized purely by the literal three-byte
// "```" sequence and its position relative to the start/end of the stream,
// exactly like stripFences looks at the start/end of the (trimmed) string
// rather than at line boundaries.
//
//   - An opening fence is resolved once, at the very start of the stream:
//     any leading whitespace is buffered, and if the first non-whitespace
//     bytes are "```", that marker is stripped immediately, followed by an
//     exact, case-sensitive "json" tag if present (mirroring stripFences'
//     literal strings.TrimPrefix(t, "json") — "```JSON"/"```javascript"
//     are NOT recognized as a tag and are left as literal text, same as
//     Generate), followed by any further leading whitespace (mirroring the
//     final strings.TrimSpace in stripFences). None of this requires a
//     newline anywhere — "```json{...}```" with no newlines at all is
//     handled the same as the more common "```json\n{...}\n```".
//   - A closing fence is only ever looked for when an opening fence WAS
//     resolved at the start of the stream — stripFences strips only when
//     BOTH ends are fenced, so a stream that never opened with "```"
//     passes through entirely verbatim, even if it happens to end in a
//     "```" marker.
//   - A closing fence is only a CANDIDATE until it's known to terminate the
//     stream: found via the same technique the "```" is searched for
//     anywhere in the remaining body (not just at a line start), it (and
//     any whitespace-only bytes after it) is buffered, not emitted, until
//     either non-whitespace content arrives (the candidate wasn't terminal
//     after all — flush it verbatim as ordinary text, then resume
//     scanning for the next "```" from that point) or the stream ends with
//     nothing but whitespace having followed the candidate (it WAS the
//     terminal closing fence — discard it, buffered whitespace included).
//   - Any other "```" occurring in the body — one that isn't part of the
//     resolved opening and doesn't turn out to be the terminal closing
//     fence — passes through unchanged, same as prose-embedded fences
//     under Generate's rule.
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
// A fence marker split across any number of deltas is still recognized
// correctly in both directions, since the relevant undecided/pending bytes
// carry over between feeds.
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
// output for it, e.g. it was wholly a fence marker). Non-text parts pass
// through unchanged and reset the scanner's opening-fence tracking,
// mirroring ExtractReasoningMiddleware's stream boundary handling.
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

// fencePhase values, in the order a stream moves through them (monotonic —
// a scanner never moves back to an earlier phase). The first four resolve
// the opening fence once, at the very start of the stream; phaseBody then
// handles the rest of the stream, including closing-fence detection.
const (
	phaseLeadWS   = iota // buffering leading whitespace, deciding if a "```" opening fence follows
	phaseOpenTick        // 1-2 backticks seen, deciding if a 3rd completes "```"
	phaseTagCheck        // opening fence confirmed; buffering up to 4 bytes to check for an exact "json" tag
	phaseTrailWS         // opening fence (+ "json" tag, if matched) confirmed; discarding whitespace before the body starts
	phaseBody            // general body scanning, including closing-fence candidate detection
)

// fenceMarker is the literal three-backtick fence marker fenceScanner looks
// for, both at the start of the stream (opening fence) and anywhere in the
// body (candidate closing fence).
var fenceMarker = []byte("```")

// fenceScanner incrementally strips markdown code-fence markers from a byte
// stream, emitting everything else via emit. It mirrors stripFences'
// whole-text semantics (see ExtractJSONMiddleware's doc comment) as closely
// as streaming allows, working purely off the literal "```" byte sequence
// and its position relative to the start/end of the stream — there is no
// notion of "lines" here, matching stripFences (which trims and checks
// prefix/suffix on the whole string, not per line):
//
//   - Leading whitespace, then (if present) a literal "```" followed by an
//     exact "json" tag and more whitespace, are all resolved once at the
//     very start of the stream and, if matched, discarded immediately —
//     this never depends on what follows, so no buffering beyond the
//     handful of bytes needed to recognize "```"/"json" themselves is ever
//     needed for the opening side.
//   - If no opening fence was resolved, the body streams through entirely
//     verbatim — stripFences strips only when BOTH ends are fenced, so an
//     unopened stream ending in "```" keeps that marker.
//   - Once in the body of an OPENED stream, any "```" found is only a
//     CANDIDATE closing fence:
//     it (plus any whitespace-only bytes after it) is buffered, not
//     emitted, until either non-whitespace content arrives (not terminal
//     after all — flush the candidate verbatim as ordinary text, then keep
//     scanning for the next "```" from that point) or the stream ends with
//     nothing but whitespace having followed it (it WAS the terminal
//     closing fence — discard it, buffered whitespace included).
//
// At most one candidate closing-fence marker (plus whatever whitespace
// follows it, until resolved) is ever buffered; everything else streams
// through with no more than a few bytes of lookback.
type fenceScanner struct {
	phase  int
	opened bool // an opening "```" was resolved at the start of the stream; closing-fence candidacy applies only when true

	lead  []byte // phaseLeadWS: buffered leading whitespace, pending resolution
	ticks []byte // phaseOpenTick: buffered backticks (1-2) seen so far
	tag   []byte // phaseTagCheck: buffered bytes (<4) being checked against "json"

	buf               []byte // phaseBody: rolling buffer — an unresolved possible-"whitespace*+```" suffix when !candidateActive, or a confirmed candidate span (preceding whitespace + marker + trailing bytes pending resolution) when candidateActive
	candidateActive   bool
	candidateMarkerAt int // phaseBody, candidateActive: offset of the "```" marker within buf (bytes before it are held-back preceding whitespace)

	emit func(string)
}

func newFenceScanner(emit func(string)) *fenceScanner {
	return &fenceScanner{emit: emit}
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

// feed advances the scanner through data, in whichever phase it's currently
// in. The pre-body phases (leading whitespace / opening "```" / "json" tag /
// trailing whitespace) are resolved byte-by-byte, since each byte can flip
// the outcome; once phaseBody is reached (immediately, if the stream never
// had an opening fence to resolve), any remaining bytes in this call are
// handed to bodyFeed in bulk.
func (s *fenceScanner) feed(data []byte) {
	i := 0
	for i < len(data) && s.phase != phaseBody {
		b := data[i]
		i++
		switch s.phase {
		case phaseLeadWS:
			if isFenceWhitespace(b) {
				s.lead = append(s.lead, b)
				continue
			}
			if b == '`' {
				s.ticks = append(s.ticks, b)
				s.phase = phaseOpenTick
				continue
			}
			// Not a fence: whatever was buffered as possible leading
			// whitespace, plus this byte, is ordinary text after all.
			s.emit(string(s.lead) + string(b))
			s.lead = nil
			s.phase = phaseBody

		case phaseOpenTick:
			if b == '`' {
				s.ticks = append(s.ticks, b)
				if len(s.ticks) == 3 {
					// Confirmed opening fence marker: the leading
					// whitespace and the marker itself are both discarded,
					// mirroring stripFences' TrimSpace + TrimPrefix("```").
					s.lead = nil
					s.ticks = nil
					s.opened = true
					s.phase = phaseTagCheck
				}
				continue
			}
			// Fewer than 3 backticks, followed by something else: not a
			// fence marker after all. Flush the buffered whitespace,
			// backticks, and this byte, verbatim.
			s.emit(string(s.lead) + string(s.ticks) + string(b))
			s.lead = nil
			s.ticks = nil
			s.phase = phaseBody

		case phaseTagCheck:
			if len(s.tag) == 0 && isFenceWhitespace(b) {
				// The very first byte right after the opening fence is
				// whitespace: "json" can never match (it starts with 'j'),
				// and per stripFences' final TrimSpace this whitespace (and
				// any more that follows) is trimmed away rather than kept
				// as literal content — reprocess it as trailing whitespace
				// instead of buffering it as a (doomed) tag attempt.
				s.phase = phaseTrailWS
				i--
				continue
			}
			s.tag = append(s.tag, b)
			if len(s.tag) < 4 {
				continue
			}
			if string(s.tag) == "json" {
				// Exact, case-sensitive match, mirroring
				// strings.TrimPrefix(t, "json") — "JSON"/"jsonl"/anything
				// else is NOT a recognized tag and is left as literal text
				// (the else branch below), same as Generate.
				s.tag = nil
				s.phase = phaseTrailWS
			} else {
				s.emit(string(s.tag))
				s.tag = nil
				s.phase = phaseBody
			}

		case phaseTrailWS:
			if isFenceWhitespace(b) {
				continue // discarded, mirroring the final TrimSpace in stripFences
			}
			s.phase = phaseBody
			i-- // reprocess this (non-whitespace) byte as the start of the body
		}
	}
	if i < len(data) && s.phase == phaseBody {
		s.bodyFeed(data[i:])
	}
}

// bodyFeed scans data for "```" occurrences (anywhere, not line-anchored),
// flushing safe text immediately and buffering only:
//
//   - when candidateActive is false: an unresolved suffix that could still
//     turn into "whitespace* + ```" once more data arrives (candidateMarkerAt
//     is unused in this state);
//   - when candidateActive is true: a confirmed candidate closing-fence
//     span — any whitespace immediately BEFORE the "```" (mirroring
//     stripFences' TrimSpace trimming trailing whitespace immediately
//     before the removed suffix), the marker itself (at offset
//     candidateMarkerAt within buf), and any whitespace observed AFTER it
//     so far (mirroring TrimSpace's leading-trim on whatever, if anything,
//     came after — here, nothing may ever come after, which is what makes
//     it terminal).
//
// Preceding whitespace must be held back speculatively (not flushed as soon
// as it's seen) because it might turn out to immediately precede a "```"
// arriving in a later delta — by the time that marker is found, already-
// flushed bytes can no longer be un-flushed.
func (s *fenceScanner) bodyFeed(data []byte) {
	if !s.opened {
		// No opening fence was resolved at the start of the stream:
		// stripFences strips only when BOTH ends are fenced, so this text
		// passes through entirely verbatim — no closing-fence candidacy,
		// no buffering.
		s.emit(string(data))
		return
	}
	s.buf = append(s.buf, data...)
	for {
		if s.candidateActive {
			i := s.candidateMarkerAt + len(fenceMarker)
			for i < len(s.buf) && isFenceWhitespace(s.buf[i]) {
				i++
			}
			if i == len(s.buf) {
				// Still unresolved (marker, possibly followed by
				// whitespace only, with no non-whitespace byte seen yet):
				// wait for more data.
				return
			}
			// s.buf[i] is non-whitespace: the candidate was not, after
			// all, the terminal closing fence. Flush the whole span
			// (preceding whitespace + marker + following whitespace)
			// verbatim, then keep scanning for the next "```" starting
			// from the triggering byte.
			flushed := string(s.buf[:i])
			rest := append([]byte(nil), s.buf[i:]...)
			s.buf = rest
			s.candidateActive = false
			s.candidateMarkerAt = 0
			s.emit(flushed)
			continue
		}

		idx := bytes.Index(s.buf, fenceMarker)
		if idx >= 0 {
			// Extend the candidate span backward over any whitespace run
			// immediately preceding the marker — that whitespace's fate
			// (discarded vs. flushed) is decided together with the
			// marker's, not before.
			start := idx
			for start > 0 && isFenceWhitespace(s.buf[start-1]) {
				start--
			}
			if start > 0 {
				s.emit(string(s.buf[:start]))
			}
			s.buf = s.buf[start:]
			s.candidateMarkerAt = idx - start
			s.candidateActive = true
			continue
		}

		// No complete "```" found yet. The unresolved tail that must be
		// held back is: an incomplete backtick-prefix of the marker (0-2
		// trailing backticks — 3 would have been found above), plus any
		// whitespace run immediately before THAT, since a "```" arriving
		// in a later delta could turn that whitespace into a preceding-
		// candidate span too.
		end := len(s.buf)
		tickStart := end
		for tickStart > 0 && s.buf[tickStart-1] == '`' && end-tickStart < len(fenceMarker)-1 {
			tickStart--
		}
		wsStart := tickStart
		for wsStart > 0 && isFenceWhitespace(s.buf[wsStart-1]) {
			wsStart--
		}
		if wsStart > 0 {
			s.emit(string(s.buf[:wsStart]))
		}
		s.buf = s.buf[wsStart:]
		return
	}
}

// finish flushes or discards whatever's pending at the end of input,
// resolving whichever phase the scanner is currently in:
//
//   - phaseLeadWS/phaseOpenTick: the buffered whitespace/backticks never
//     completed into a fence marker (or the stream ended before 3
//     backticks were confirmed) — emitted as ordinary text, verbatim.
//   - phaseTagCheck: a confirmed opening fence was followed by fewer than 4
//     more bytes, too few to be conclusively "json" or not — emitted as
//     ordinary text, verbatim (it can't have been the "json" tag).
//   - phaseTrailWS: only whitespace followed a confirmed opening fence (or
//     "```json" tag) before the stream ended — fully absorbed, nothing to
//     flush, mirroring stripFences' TrimSpace.
//   - phaseBody: delegated to bodyFinish.
func (s *fenceScanner) finish() {
	switch s.phase {
	case phaseLeadWS:
		if len(s.lead) > 0 {
			s.emit(string(s.lead))
			s.lead = nil
		}
	case phaseOpenTick:
		if len(s.lead) > 0 || len(s.ticks) > 0 {
			s.emit(string(s.lead) + string(s.ticks))
			s.lead = nil
			s.ticks = nil
		}
	case phaseTagCheck:
		if len(s.tag) > 0 {
			s.emit(string(s.tag))
			s.tag = nil
		}
	case phaseTrailWS:
		// Nothing buffered in this phase; whitespace is discarded as it's
		// consumed in feed.
	case phaseBody:
		s.bodyFinish()
	}
}

// bodyFinish resolves whatever bodyFeed left pending at the end of input:
//
//   - candidateActive: the buffered "```" (plus any whitespace since)
//     reached the end of the stream with nothing else following — it IS
//     the terminal closing fence, so it's discarded, whitespace included,
//     mirroring stripFences removing the trailing "```" and the whitespace
//     TrimSpace would have absorbed around it.
//   - otherwise: whatever's left in buf is an unresolved "```"-prefix that
//     never completed (e.g. a lone "“" at the very end) — emitted as
//     ordinary text, verbatim.
func (s *fenceScanner) bodyFinish() {
	if s.candidateActive {
		s.buf = nil
		s.candidateActive = false
		return
	}
	if len(s.buf) > 0 {
		s.emit(string(s.buf))
		s.buf = nil
	}
}
