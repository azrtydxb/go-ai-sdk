package ai

import (
	"bytes"
	"context"
	"iter"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---------------------------------------------------------------------
// ExtractReasoningMiddleware
// ---------------------------------------------------------------------

// ExtractReasoningMiddleware wraps model so that <tagName>...</tagName>
// spans embedded in its text output are pulled out and re-emitted as
// reasoning content (provider.ReasoningPart in Generate responses,
// provider.ReasoningDelta/ReasoningEnd in streams) instead of ordinary
// text. This is useful for models that signal "thinking" with an inline
// tag in the text stream rather than a dedicated reasoning content type
// (e.g. some DeepSeek-compatible endpoints using "<think>...</think>").
//
// tagName is the tag name without angle brackets, e.g. "think".
//
// Some models omit the opening tag entirely and begin their response
// already "inside" the reasoning span, relying solely on the closing tag
// to mark the transition to normal output (e.g. a raw
// "Let me work through this... </think> The answer is 4."). This is
// detected automatically: if a closing tag is encountered before any
// opening tag has been seen, everything received so far in the call is
// treated as reasoning text retroactively. Because this determination
// can only be made once we know whether the response ever contains the
// tag markers, the very first span of a call is buffered in full until
// the ambiguity resolves (i.e. until the first tag boundary of either
// kind is found, or the call/stream ends without one). After that first
// resolution, scanning reverts to bounded buffering: only the longest
// unresolved prefix of the tag currently being watched for is held back,
// so tag markers split across stream deltas are still recognized without
// buffering unrelated content.
func ExtractReasoningMiddleware(model provider.LanguageModel, tagName string) provider.LanguageModel {
	return &extractReasoningModel{model: model, tagName: tagName}
}

type extractReasoningModel struct {
	model   provider.LanguageModel
	tagName string
}

func (m *extractReasoningModel) ModelID() string                     { return m.model.ModelID() }
func (m *extractReasoningModel) ProviderName() string                { return m.model.ProviderName() }
func (m *extractReasoningModel) Capabilities() provider.Capabilities { return m.model.Capabilities() }

func (m *extractReasoningModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	resp, err := m.model.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return extractReasoningFromResponse(resp, m.tagName), nil
}

func (m *extractReasoningModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.model.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	return &extractReasoningStream{inner: inner, tagName: m.tagName}, nil
}

// extractReasoningFromResponse rewrites resp's TextParts, splitting out any
// <tagName>...</tagName> spans into ReasoningParts. Other content part
// types pass through unchanged, in their original position.
func extractReasoningFromResponse(resp *provider.Response, tagName string) *provider.Response {
	var newContent []provider.ContentPart
	for _, part := range resp.Content {
		tp, ok := part.(provider.TextPart)
		if !ok {
			newContent = append(newContent, part)
			continue
		}
		newContent = append(newContent, extractReasoningParts(tp.Text, tagName)...)
	}
	return &provider.Response{
		Content:      newContent,
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
		Raw:          resp.Raw,
	}
}

// extractReasoningParts scans text for tagName spans and returns an
// ordered slice of TextPart/ReasoningPart content parts.
func extractReasoningParts(text, tagName string) []provider.ContentPart {
	var parts []provider.ContentPart
	var textBuf, reasonBuf strings.Builder

	flushText := func() {
		if textBuf.Len() > 0 {
			parts = append(parts, provider.TextPart{Text: textBuf.String()})
			textBuf.Reset()
		}
	}
	flushReason := func() {
		if reasonBuf.Len() > 0 {
			parts = append(parts, provider.ReasoningPart{Text: reasonBuf.String()})
			reasonBuf.Reset()
		}
	}

	sink := reasoningSink{
		text: func(s string) {
			flushReason()
			textBuf.WriteString(s)
		},
		reasoningText: func(s string) {
			flushText()
			reasonBuf.WriteString(s)
		},
		reasoningClose: func() {
			flushReason()
		},
	}

	sc := newReasoningScanner(tagName, sink)
	sc.feed([]byte(text))
	sc.finish()
	flushText()
	flushReason()

	return parts
}

// extractReasoningStream wraps a provider.StreamResponse, running its
// TextDeltas through a reasoningScanner and translating the result into
// TextDelta/ReasoningDelta/ReasoningEnd. Non-text parts pass through
// unchanged.
type extractReasoningStream struct {
	inner   provider.StreamResponse
	tagName string
}

func (s *extractReasoningStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		var reasoningAccum strings.Builder
		stopped := false
		emit := func(p provider.StreamPart) {
			if stopped {
				return
			}
			if !yield(p) {
				stopped = true
			}
		}

		sink := reasoningSink{
			text: func(s string) {
				if s == "" {
					return
				}
				emit(provider.TextDelta{Text: s})
			},
			reasoningText: func(s string) {
				if s == "" {
					return
				}
				reasoningAccum.WriteString(s)
				emit(provider.ReasoningDelta{Text: s})
			},
			reasoningClose: func() {
				part := provider.ReasoningPart{Text: reasoningAccum.String()}
				reasoningAccum.Reset()
				emit(provider.ReasoningEnd{Part: part})
			},
		}

		sc := newReasoningScanner(s.tagName, sink)

		for p := range s.inner.Parts() {
			if stopped {
				break
			}
			if td, ok := p.(provider.TextDelta); ok {
				sc.feed([]byte(td.Text))
			} else {
				// A non-text part (e.g. ToolCallEnd, FinishPart) marks a
				// boundary: flush any text/reasoning buffered so far so
				// it's emitted in order before this part, then start a
				// fresh scan for any further TextDeltas.
				sc.finish()
				sc = newReasoningScanner(s.tagName, sink)
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

func (s *extractReasoningStream) Err() error   { return s.inner.Err() }
func (s *extractReasoningStream) Close() error { return s.inner.Close() }

// reasoningSink receives classified output from a reasoningScanner.
type reasoningSink struct {
	text           func(string)
	reasoningText  func(string)
	reasoningClose func()
}

// reasoningScanner incrementally splits a byte stream into plain-text and
// <tagName>...</tagName> reasoning spans. See ExtractReasoningMiddleware's
// doc comment for the two-phase buffering strategy (unbounded until the
// first tag boundary resolves, then bounded to the tag's own length).
type reasoningScanner struct {
	openTag  []byte
	closeTag []byte

	resolved    bool
	inReasoning bool
	buf         []byte

	sink reasoningSink
}

func newReasoningScanner(tagName string, sink reasoningSink) *reasoningScanner {
	return &reasoningScanner{
		openTag:  []byte("<" + tagName + ">"),
		closeTag: []byte("</" + tagName + ">"),
		sink:     sink,
	}
}

func (s *reasoningScanner) feed(data []byte) {
	s.buf = append(s.buf, data...)
	for {
		if !s.resolved {
			oIdx := bytes.Index(s.buf, s.openTag)
			cIdx := bytes.Index(s.buf, s.closeTag)
			switch {
			case oIdx == -1 && cIdx == -1:
				// Ambiguous: keep buffering until we know whether this
				// call ever mentions the tag.
				return
			case oIdx != -1 && (cIdx == -1 || oIdx <= cIdx):
				if oIdx > 0 {
					s.sink.text(string(s.buf[:oIdx]))
				}
				s.buf = s.buf[oIdx+len(s.openTag):]
				s.inReasoning = true
				s.resolved = true
				continue
			default:
				// Closing tag found with no preceding opening tag: the
				// model started the response already "inside" reasoning.
				if cIdx > 0 {
					s.sink.reasoningText(string(s.buf[:cIdx]))
				}
				s.sink.reasoningClose()
				s.buf = s.buf[cIdx+len(s.closeTag):]
				s.inReasoning = false
				s.resolved = true
				continue
			}
		}

		watch := s.openTag
		if s.inReasoning {
			watch = s.closeTag
		}
		idx := bytes.Index(s.buf, watch)
		if idx >= 0 {
			seg := s.buf[:idx]
			if len(seg) > 0 {
				if s.inReasoning {
					s.sink.reasoningText(string(seg))
				} else {
					s.sink.text(string(seg))
				}
			}
			s.buf = s.buf[idx+len(watch):]
			if s.inReasoning {
				s.sink.reasoningClose()
				s.inReasoning = false
			} else {
				s.inReasoning = true
			}
			continue
		}

		k := longestSuffixPrefixOverlap(s.buf, watch)
		safe := s.buf[:len(s.buf)-k]
		if len(safe) > 0 {
			if s.inReasoning {
				s.sink.reasoningText(string(safe))
			} else {
				s.sink.text(string(safe))
			}
		}
		s.buf = s.buf[len(s.buf)-k:]
		return
	}
}

// finish flushes any remaining buffered content at the end of input.
func (s *reasoningScanner) finish() {
	if !s.resolved {
		// The tag never appeared: treat everything buffered as ordinary
		// text.
		if len(s.buf) > 0 {
			s.sink.text(string(s.buf))
		}
		s.buf = nil
		return
	}
	if len(s.buf) > 0 {
		if s.inReasoning {
			s.sink.reasoningText(string(s.buf))
		} else {
			s.sink.text(string(s.buf))
		}
	}
	s.buf = nil
}

// longestSuffixPrefixOverlap returns the length of the longest proper
// suffix of buf that is also a prefix of watch (0 if none), i.e. how many
// trailing bytes of buf must be held back because they could still grow
// into a full match of watch.
func longestSuffixPrefixOverlap(buf, watch []byte) int {
	max := len(watch) - 1
	if max > len(buf) {
		max = len(buf)
	}
	for k := max; k > 0; k-- {
		if bytes.Equal(buf[len(buf)-k:], watch[:k]) {
			return k
		}
	}
	return 0
}

// ---------------------------------------------------------------------
// SimulateStreamingMiddleware
// ---------------------------------------------------------------------

// SimulateStreamingMiddleware makes model's Stream method call Generate
// instead, then replay the resulting Response as a synthetic single-chunk
// stream. This lets code written against the streaming API work uniformly
// with models/providers that only support non-streaming Generate.
//
// The synthetic stream emits, in order: for each ReasoningPart in the
// response, a ReasoningDelta carrying its text followed by a ReasoningEnd
// carrying the part itself; then a TextDelta for each TextPart; then a
// ToolCallEnd for each tool call; then a single FinishPart carrying the
// response's FinishReason and Usage.
func SimulateStreamingMiddleware(model provider.LanguageModel) provider.LanguageModel {
	return &simulateStreamingModel{model: model}
}

type simulateStreamingModel struct {
	model provider.LanguageModel
}

func (m *simulateStreamingModel) ModelID() string      { return m.model.ModelID() }
func (m *simulateStreamingModel) ProviderName() string { return m.model.ProviderName() }
func (m *simulateStreamingModel) Capabilities() provider.Capabilities {
	return m.model.Capabilities()
}

func (m *simulateStreamingModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.model.Generate(ctx, call)
}

func (m *simulateStreamingModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	resp, err := m.model.Generate(ctx, call)
	if err != nil {
		return nil, err
	}

	var parts []provider.StreamPart
	for _, part := range resp.Content {
		if rp, ok := part.(provider.ReasoningPart); ok {
			if rp.Text != "" {
				parts = append(parts, provider.ReasoningDelta{Text: rp.Text})
			}
			parts = append(parts, provider.ReasoningEnd{Part: rp})
		}
	}
	for _, part := range resp.Content {
		if tp, ok := part.(provider.TextPart); ok {
			parts = append(parts, provider.TextDelta{Text: tp.Text})
		}
	}
	for _, part := range resp.Content {
		if tc, ok := part.(provider.ToolCallPart); ok {
			parts = append(parts, provider.ToolCallEnd{Call: tc})
		}
	}
	parts = append(parts, provider.FinishPart{Reason: resp.FinishReason, Usage: resp.Usage})

	return &simulatedStreamResponse{parts: parts}, nil
}

type simulatedStreamResponse struct {
	parts []provider.StreamPart
}

func (s *simulatedStreamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		for _, p := range s.parts {
			if !yield(p) {
				return
			}
		}
	}
}

func (s *simulatedStreamResponse) Err() error   { return nil }
func (s *simulatedStreamResponse) Close() error { return nil }

// ---------------------------------------------------------------------
// DefaultSettingsMiddleware
// ---------------------------------------------------------------------

// DefaultSettingsMiddleware wraps model so that every call's zero-valued
// fields are filled in from defaults before being sent to the underlying
// model: Temperature/TopP/MaxTokens (nil pointers), StopSequences (empty
// slice), and ProviderOptions (merged per provider-name namespace, with
// per-call entries winning over the matching default entries). All other
// Call fields (Messages, Tools, ToolChoice, ResponseFormat) are passed
// through unmodified — per-call values always win because only
// zero-valued fields are ever replaced.
func DefaultSettingsMiddleware(model provider.LanguageModel, defaults provider.Call) provider.LanguageModel {
	return &defaultSettingsModel{model: model, defaults: defaults}
}

type defaultSettingsModel struct {
	model    provider.LanguageModel
	defaults provider.Call
}

func (m *defaultSettingsModel) ModelID() string                     { return m.model.ModelID() }
func (m *defaultSettingsModel) ProviderName() string                { return m.model.ProviderName() }
func (m *defaultSettingsModel) Capabilities() provider.Capabilities { return m.model.Capabilities() }

func (m *defaultSettingsModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.model.Generate(ctx, m.applyDefaults(call))
}

func (m *defaultSettingsModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	return m.model.Stream(ctx, m.applyDefaults(call))
}

func (m *defaultSettingsModel) applyDefaults(call provider.Call) provider.Call {
	if call.Temperature == nil {
		call.Temperature = m.defaults.Temperature
	}
	if call.TopP == nil {
		call.TopP = m.defaults.TopP
	}
	if call.MaxTokens == nil {
		call.MaxTokens = m.defaults.MaxTokens
	}
	if len(call.StopSequences) == 0 {
		call.StopSequences = m.defaults.StopSequences
	}
	call.ProviderOptions = mergeProviderOptions(m.defaults.ProviderOptions, call.ProviderOptions)
	return call
}

// mergeProviderOptions shallow-merges per-call provider options over
// defaults, per provider-name namespace: within a namespace, per-call
// entries win over the matching default entries; namespaces present only
// in defaults are carried through unchanged.
func mergeProviderOptions(defaults, override map[string]any) map[string]any {
	if len(defaults) == 0 {
		return override
	}
	if len(override) == 0 {
		return defaults
	}

	merged := make(map[string]any, len(defaults)+len(override))
	for k, v := range defaults {
		merged[k] = v
	}
	for ns, ov := range override {
		dv, ok := merged[ns]
		if !ok {
			merged[ns] = ov
			continue
		}
		dm, dOk := dv.(map[string]any)
		om, oOk := ov.(map[string]any)
		if !dOk || !oOk {
			// Not both maps: per-call value wins outright.
			merged[ns] = ov
			continue
		}
		nsMerged := make(map[string]any, len(dm)+len(om))
		for k, v := range dm {
			nsMerged[k] = v
		}
		for k, v := range om {
			nsMerged[k] = v
		}
		merged[ns] = nsMerged
	}
	return merged
}
