package ai

import (
	"bytes"
	"context"
	"iter"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---------------------------------------------------------------------
// AddToolInputExamplesMiddleware
// ---------------------------------------------------------------------

// AddToolInputExamplesMiddleware wraps model so that every outgoing Call's
// tools have their InputExamples folded into their Description as text,
// then cleared. For each tool with a non-empty InputExamples, it appends
// "\n\nExample inputs:\n" followed by each example's compact JSON on its
// own line to the tool's Description, then clears InputExamples so a
// provider with native support for the field (e.g. Anthropic's
// input_examples) doesn't also receive — and double-count — the same
// examples via its wire field. This mirrors the AI SDK v6 middleware that
// serializes examples into description text for providers without native
// support.
//
// The wrapped Call's Tools is always a fresh slice; the caller's original
// Tools slice (and its ToolDef values) are never mutated, so calling this
// middleware repeatedly (or wrapping an already-wrapped model) is
// idempotent per call — each invocation starts again from the original
// Description plus the original InputExamples supplied by the caller.
func AddToolInputExamplesMiddleware(model provider.LanguageModel) provider.LanguageModel {
	if model == nil {
		panic("ai: AddToolInputExamplesMiddleware: nil model")
	}
	return &addToolInputExamplesModel{model: model}
}

type addToolInputExamplesModel struct {
	model provider.LanguageModel
}

func (m *addToolInputExamplesModel) ModelID() string      { return m.model.ModelID() }
func (m *addToolInputExamplesModel) ProviderName() string { return m.model.ProviderName() }
func (m *addToolInputExamplesModel) Capabilities() provider.Capabilities {
	return m.model.Capabilities()
}

func (m *addToolInputExamplesModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	return m.model.Generate(ctx, foldToolInputExamples(call))
}

func (m *addToolInputExamplesModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	return m.model.Stream(ctx, foldToolInputExamples(call))
}

// foldToolInputExamples returns a copy of call whose Tools slice has each
// tool's InputExamples folded into its Description and cleared. Tools with
// no InputExamples are copied through unchanged. call.Tools itself is never
// mutated.
func foldToolInputExamples(call provider.Call) provider.Call {
	if len(call.Tools) == 0 {
		return call
	}
	tools := make([]provider.ToolDef, len(call.Tools))
	for i, t := range call.Tools {
		if len(t.InputExamples) == 0 {
			tools[i] = t
			continue
		}
		var b strings.Builder
		b.WriteString(t.Description)
		b.WriteString("\n\nExample inputs:\n")
		for j, ex := range t.InputExamples {
			if j > 0 {
				b.WriteString("\n")
			}
			b.Write(ex)
		}
		t.Description = b.String()
		t.InputExamples = nil
		tools[i] = t
	}
	call.Tools = tools
	return call
}

// ---------------------------------------------------------------------
// ExtractReasoningMiddleware
// ---------------------------------------------------------------------

// ExtractReasoningOpts configures ExtractReasoningMiddleware.
type ExtractReasoningOpts struct {
	// TagName is the tag name without angle brackets, e.g. "think".
	// Required.
	TagName string

	// StartWithReasoning indicates the model omits the opening tag and
	// begins its response already "inside" the reasoning span, relying
	// solely on the closing tag to mark the transition to normal output
	// (e.g. a raw "Let me work through this... </think> The answer is
	// 4."). When true, content from the very start of the call/stream is
	// treated as reasoning until the closing tag is seen (or, if it never
	// arrives, for the whole response). When false (the default), an
	// orphan closing tag with no matching opener is inert: it passes
	// through as ordinary text verbatim.
	StartWithReasoning bool
}

// ExtractReasoningMiddleware wraps model so that <tagName>...</tagName>
// spans embedded in its text output are pulled out and re-emitted as
// reasoning content (provider.ReasoningPart in Generate responses,
// provider.ReasoningDelta/ReasoningEnd in streams) instead of ordinary
// text. This is useful for models that signal "thinking" with an inline
// tag in the text stream rather than a dedicated reasoning content type
// (e.g. some DeepSeek-compatible endpoints using "<think>...</think>").
//
// The stream path is fully incremental: it never buffers more than the
// longest unresolved prefix of the tag currently being watched for (open
// tag while outside reasoning, close tag while inside), so text/reasoning
// content flows to the caller as it arrives rather than being held back
// pending a later determination. Tag markers split across stream deltas
// (e.g. "<th" then "ink>") are still recognized correctly since the
// unresolved prefix carries over between feeds.
func ExtractReasoningMiddleware(model provider.LanguageModel, opts ExtractReasoningOpts) provider.LanguageModel {
	if model == nil {
		panic("ai: ExtractReasoningMiddleware: nil model")
	}
	return &extractReasoningModel{model: model, opts: opts}
}

type extractReasoningModel struct {
	model provider.LanguageModel
	opts  ExtractReasoningOpts
}

func (m *extractReasoningModel) ModelID() string                     { return m.model.ModelID() }
func (m *extractReasoningModel) ProviderName() string                { return m.model.ProviderName() }
func (m *extractReasoningModel) Capabilities() provider.Capabilities { return m.model.Capabilities() }

func (m *extractReasoningModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	resp, err := m.model.Generate(ctx, call)
	if err != nil {
		return nil, err
	}
	return extractReasoningFromResponse(resp, m.opts), nil
}

func (m *extractReasoningModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	inner, err := m.model.Stream(ctx, call)
	if err != nil {
		return nil, err
	}
	return &extractReasoningStream{inner: inner, opts: m.opts}, nil
}

// extractReasoningFromResponse rewrites resp's TextParts, splitting out any
// <tagName>...</tagName> spans into ReasoningParts. Other content part
// types pass through unchanged, in their original position.
func extractReasoningFromResponse(resp *provider.Response, opts ExtractReasoningOpts) *provider.Response {
	var newContent []provider.ContentPart
	for _, part := range resp.Content {
		tp, ok := part.(provider.TextPart)
		if !ok {
			newContent = append(newContent, part)
			continue
		}
		newContent = append(newContent, extractReasoningParts(tp.Text, opts)...)
	}
	return &provider.Response{
		Content:          newContent,
		FinishReason:     resp.FinishReason,
		Usage:            resp.Usage,
		Raw:              resp.Raw,
		ProviderMetadata: resp.ProviderMetadata,
	}
}

// extractReasoningParts scans text for tagName spans and returns an
// ordered slice of TextPart/ReasoningPart content parts.
func extractReasoningParts(text string, opts ExtractReasoningOpts) []provider.ContentPart {
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

	sc := newReasoningScanner(opts.TagName, opts.StartWithReasoning, sink)
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
	inner provider.StreamResponse
	opts  ExtractReasoningOpts
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

		sc := newReasoningScanner(s.opts.TagName, s.opts.StartWithReasoning, sink)

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
				// fresh scan (back in the configured start state) for any
				// further TextDeltas.
				sc.finish()
				sc = newReasoningScanner(s.opts.TagName, s.opts.StartWithReasoning, sink)
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
// <tagName>...</tagName> reasoning spans, starting in the "reasoning" state
// when startWithReasoning is true (position 0 counts as already inside an
// open span) or the "outside" state otherwise. It never buffers more than
// the longest unresolved prefix of the tag it is currently watching for
// (open tag while outside, close tag while inside): content that cannot
// possibly be part of that tag is flushed to the sink immediately, so
// scanning is fully incremental with no unbounded buffering. In the
// default (startWithReasoning=false) outside state, the scanner only ever
// watches for the opening tag, so a closing tag with no matching opener is
// never recognized as a boundary — it flows through as ordinary text, byte
// for byte, once it's clear it can't be an opening-tag prefix.
type reasoningScanner struct {
	openTag  []byte
	closeTag []byte

	inReasoning bool
	buf         []byte

	sink reasoningSink
}

func newReasoningScanner(tagName string, startWithReasoning bool, sink reasoningSink) *reasoningScanner {
	return &reasoningScanner{
		openTag:     []byte("<" + tagName + ">"),
		closeTag:    []byte("</" + tagName + ">"),
		inReasoning: startWithReasoning,
		sink:        sink,
	}
}

func (s *reasoningScanner) feed(data []byte) {
	s.buf = append(s.buf, data...)
	for {
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
			}
			s.inReasoning = !s.inReasoning
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

// finish flushes any remaining buffered content (an unresolved tag-prefix
// candidate that never completed) at the end of input, verbatim, in
// whatever state the scanner is currently in.
func (s *reasoningScanner) finish() {
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
	if model == nil {
		panic("ai: SimulateStreamingMiddleware: nil model")
	}
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
	parts = append(parts, provider.FinishPart{
		Reason:           resp.FinishReason,
		Usage:            resp.Usage,
		ProviderMetadata: resp.ProviderMetadata,
	})

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
// model: Temperature/TopP/MaxTokens/TopK/PresencePenalty/FrequencyPenalty/
// Seed/Reasoning (nil pointers), StopSequences (empty slice), Headers (merged per
// header key, with per-call keys winning over the matching default key —
// same semantics as ProviderOptions below, applied one level shallower
// since Headers has no namespace level), and ProviderOptions (merged per
// provider-name namespace, with per-call entries winning over the matching
// default entries). All other Call fields (Messages, Tools, ToolChoice,
// ResponseFormat) are passed through unmodified — per-call values always
// win because only zero-valued fields are ever replaced/merged in the
// caller's favor.
func DefaultSettingsMiddleware(model provider.LanguageModel, defaults provider.Call) provider.LanguageModel {
	if model == nil {
		panic("ai: DefaultSettingsMiddleware: nil model")
	}
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
	if call.TopK == nil {
		call.TopK = m.defaults.TopK
	}
	if call.PresencePenalty == nil {
		call.PresencePenalty = m.defaults.PresencePenalty
	}
	if call.FrequencyPenalty == nil {
		call.FrequencyPenalty = m.defaults.FrequencyPenalty
	}
	if call.Seed == nil {
		call.Seed = m.defaults.Seed
	}
	if call.Reasoning == nil {
		call.Reasoning = m.defaults.Reasoning
	}
	if len(call.StopSequences) == 0 {
		call.StopSequences = m.defaults.StopSequences
	}
	call.Headers = mergeHeaders(m.defaults.Headers, call.Headers)
	call.ProviderOptions = mergeProviderOptions(m.defaults.ProviderOptions, call.ProviderOptions)
	return call
}

// mergeHeaders merges per-call headers over defaults, per header key:
// per-call entries win over the matching default key; keys present only in
// defaults are carried through unchanged. Key matching is exact (not
// case-insensitive) — Call.Headers keys are compared case-insensitively only
// where they're consumed against a provider's fixed auth-header name, not
// against each other here. The returned map is always a fresh copy — the
// caller's defaults and override maps are never aliased, so mutating the
// result afterward cannot affect either input.
func mergeHeaders(defaults, override map[string]string) map[string]string {
	if len(defaults) == 0 {
		return copyHeadersMap(override)
	}
	if len(override) == 0 {
		return copyHeadersMap(defaults)
	}
	merged := make(map[string]string, len(defaults)+len(override))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// copyHeadersMap returns a copy of m (nil stays nil).
func copyHeadersMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

// mergeProviderOptions shallow-merges per-call provider options over
// defaults, per provider-name namespace: within a namespace, per-call
// entries win over the matching default entries; namespaces present only
// in defaults are carried through unchanged. The returned map (and any
// namespace map within it) is always a fresh copy — the caller's defaults
// and override maps are never aliased, so mutating the result afterward
// cannot affect either input.
func mergeProviderOptions(defaults, override map[string]any) map[string]any {
	if len(defaults) == 0 {
		return copyOptionsMap(override)
	}
	if len(override) == 0 {
		return copyOptionsMap(defaults)
	}

	merged := make(map[string]any, len(defaults)+len(override))
	for ns, v := range defaults {
		merged[ns] = copyNamespaceValue(v)
	}
	for ns, ov := range override {
		dv, ok := merged[ns]
		if !ok {
			merged[ns] = copyNamespaceValue(ov)
			continue
		}
		dm, dOk := dv.(map[string]any)
		om, oOk := ov.(map[string]any)
		if !dOk || !oOk {
			// Not both maps: per-call value wins outright.
			merged[ns] = copyNamespaceValue(ov)
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

// copyOptionsMap returns a copy of m (nil stays nil) with both the
// top-level map and every namespace map[string]any value within it freshly
// allocated, so the result never aliases m or any map nested in it.
func copyOptionsMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = copyNamespaceValue(v)
	}
	return cp
}

// copyNamespaceValue returns a shallow copy of v when it is a
// map[string]any (the common shape of a provider-options namespace value),
// otherwise v unchanged.
func copyNamespaceValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	cp := make(map[string]any, len(m))
	for k, vv := range m {
		cp[k] = vv
	}
	return cp
}
