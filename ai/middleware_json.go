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
// the caller. It reuses the same fence-stripping rule as GenerateObject's
// non-native-JSON decoding path (see stripFences): a single leading fence
// line ("```" or "```json", on its own line) and a single trailing fence
// line are removed; content that isn't fenced passes through unchanged.
//
// Generate strips fences from each TextPart of the response as a whole.
// Stream strips them incrementally: it buffers only the bytes of a
// candidate fence line (detected by a "```" prefix at the start of a line)
// long enough to decide whether that line is in fact a fence — three bytes,
// or fewer if a newline arrives first — then either discards the whole line
// (including its trailing newline) if it was a fence, or flushes the
// buffered bytes and passes everything else through as ordinary TextDeltas
// without any further buffering. A fence marker split across two deltas
// (e.g. two backticks then "`json\n") is still recognized, since the
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
	fenceAtLineStart = iota // deciding whether the current line is a fence line
	fencePassthrough        // mid-line, known not a fence line: pass bytes through
	fenceDiscarding         // mid-line, known to BE a fence line: discard bytes
)

// fenceScanner incrementally strips markdown code-fence lines (a line whose
// first three bytes are "```") from a byte stream, emitting everything else
// via emit. It never buffers more than the undecided prefix of the "```"
// marker at the start of a line (at most 2 bytes, since 3 resolves the
// decision), so scanning is fully incremental.
type fenceScanner struct {
	state int
	buf   []byte // only populated in state fenceAtLineStart
	emit  func(string)
}

func newFenceScanner(emit func(string)) *fenceScanner {
	return &fenceScanner{emit: emit}
}

func (s *fenceScanner) feed(data []byte) {
	for i := 0; i < len(data); {
		switch s.state {
		case fenceAtLineStart:
			b := data[i]
			if b == '\n' {
				// A line shorter than 3 bytes can never be a fence marker
				// ("```" requires exactly 3 backticks): flush whatever was
				// buffered plus this newline as ordinary text, and stay at
				// line start for the next line.
				if len(s.buf) > 0 {
					s.emit(string(s.buf))
					s.buf = nil
				}
				s.emit("\n")
				i++
				continue
			}
			s.buf = append(s.buf, b)
			i++
			if len(s.buf) == 3 {
				if string(s.buf) == "```" {
					s.buf = nil
					s.state = fenceDiscarding
				} else {
					flushed := string(s.buf)
					s.buf = nil
					s.emit(flushed)
					s.state = fencePassthrough
				}
			}
		case fencePassthrough:
			idx := bytes.IndexByte(data[i:], '\n')
			if idx < 0 {
				s.emit(string(data[i:]))
				i = len(data)
			} else {
				s.emit(string(data[i : i+idx+1]))
				i += idx + 1
				s.state = fenceAtLineStart
			}
		case fenceDiscarding:
			idx := bytes.IndexByte(data[i:], '\n')
			if idx < 0 {
				i = len(data)
			} else {
				i += idx + 1
				s.state = fenceAtLineStart
			}
		}
	}
}

// finish flushes any remaining buffered content at the end of input: an
// undecided fence-marker prefix (fewer than 3 bytes seen, no newline yet)
// can never complete into a fence line, so it is emitted as ordinary text,
// verbatim. A stream that ends mid-fence-line (state fenceDiscarding, no
// closing newline ever arrived) or mid-passthrough-line (nothing left
// buffered, already emitted) has nothing left to flush.
func (s *fenceScanner) finish() {
	if s.state == fenceAtLineStart && len(s.buf) > 0 {
		s.emit(string(s.buf))
		s.buf = nil
	}
}
