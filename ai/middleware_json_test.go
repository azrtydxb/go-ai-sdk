package ai

import (
	"context"
	"reflect"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---------------------------------------------------------------------
// ExtractJSONMiddleware — Generate
// ---------------------------------------------------------------------

func TestExtractJSONMiddleware_Generate_StripsFence(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{
				provider.TextPart{Text: "```json\n{\"a\":1}\n```"},
			}},
		},
	}
	wrapped := ExtractJSONMiddleware(mock)

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Content = %#v", resp.Content)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok || tp.Text != `{"a":1}` {
		t.Errorf("Content[0] = %#v", resp.Content[0])
	}
}

func TestExtractJSONMiddleware_Generate_NoFencePassesThrough(t *testing.T) {
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{provider.TextPart{Text: `{"a":1}`}}},
		},
	}
	wrapped := ExtractJSONMiddleware(mock)

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok || tp.Text != `{"a":1}` {
		t.Errorf("Content[0] = %#v", resp.Content[0])
	}
}

// TestExtractJSONMiddleware_Generate_ProseEmbeddedFencesPassThrough covers
// that stripFences' whole-text rule requires a fence at BOTH the start and
// end of the (trimmed) text: text that merely CONTAINS fence lines in the
// middle of otherwise-unfenced prose is left completely untouched, since
// neither end of the trimmed text is itself a bare "```".
func TestExtractJSONMiddleware_Generate_ProseEmbeddedFencesPassThrough(t *testing.T) {
	text := "Some prose\n```\ncode\n```\nmore prose"
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{provider.TextPart{Text: text}}},
		},
	}
	wrapped := ExtractJSONMiddleware(mock)

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok || tp.Text != text {
		t.Errorf("Content[0] = %#v, want unchanged %q", resp.Content[0], text)
	}
}

func TestExtractJSONMiddleware_Generate_NonTextPartsPassThrough(t *testing.T) {
	tc := provider.ToolCallPart{ID: "c1", Name: "t", Args: []byte(`{}`)}
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{
				provider.TextPart{Text: "```json\n{\"a\":1}\n```"},
				tc,
			}},
		},
	}
	wrapped := ExtractJSONMiddleware(mock)

	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("Content = %#v", resp.Content)
	}
	if !reflect.DeepEqual(resp.Content[1], provider.ContentPart(tc)) {
		t.Errorf("Content[1] = %#v, want unmodified tool call part", resp.Content[1])
	}
}

// ---------------------------------------------------------------------
// ExtractJSONMiddleware — Stream
// ---------------------------------------------------------------------

func TestExtractJSONMiddleware_Stream_StripsFenceInOneDelta(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "```json\n{\"a\":1}\n```"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	parts := collectStreamParts(t, sr)

	// The scanner is incremental and may split its output into more than
	// one TextDelta even for a single input delta (it flushes as soon as a
	// decision is made, rather than buffering the whole line) — assert on
	// the concatenated text and the trailing FinishPart, not exact delta
	// boundaries.
	var got string
	for _, p := range parts[:len(parts)-1] {
		td, ok := p.(provider.TextDelta)
		if !ok {
			t.Fatalf("non-TextDelta part before FinishPart: %#v", p)
		}
		got += td.Text
	}
	if got != `{"a":1}`+"\n" {
		t.Fatalf("got %q, want %q", got, `{"a":1}`+"\n")
	}
	last := parts[len(parts)-1]
	if !reflect.DeepEqual(last, provider.StreamPart(provider.FinishPart{Reason: provider.FinishStop})) {
		t.Fatalf("last part = %#v, want FinishPart{Reason: FinishStop}", last)
	}
}

// TestExtractJSONMiddleware_Stream_FenceSplitAcrossDeltas verifies the
// incremental fence-scanner correctly recognizes a fence marker even when
// its backticks are split across multiple TextDeltas.
func TestExtractJSONMiddleware_Stream_FenceSplitAcrossDeltas(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "``"},
			provider.TextDelta{Text: "`json\n"},
			provider.TextDelta{Text: `{"a"`},
			provider.TextDelta{Text: `:1}` + "\n"},
			provider.TextDelta{Text: "``"},
			provider.TextDelta{Text: "`"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != `{"a":1}`+"\n" {
		t.Fatalf("got %q, want %q", got, `{"a":1}`+"\n")
	}
}

func TestExtractJSONMiddleware_Stream_NoFencePassesThrough(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "hello "},
			provider.TextDelta{Text: "world"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}

// TestExtractJSONMiddleware_Stream_ShortLineNotMistakenForFence verifies a
// line shorter than 3 bytes (which can never be a "```" marker) passes
// through untouched, including its newline.
func TestExtractJSONMiddleware_Stream_ShortLineNotMistakenForFence(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "``\nrest"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != "``\nrest" {
		t.Fatalf("got %q, want %q", got, "``\nrest")
	}
}

func TestExtractJSONMiddleware_Stream_UnclosedFenceMarkerAtEndFlushed(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "hi``"}, // trailing "``" never resolves to "```"
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != "hi``" {
		t.Fatalf("got %q, want %q", got, "hi``")
	}
}

// TestExtractJSONMiddleware_Stream_ProseEmbeddedFencesPassThrough is the
// Stream counterpart to the Generate prose-embedded-fences test: neither the
// first fence line (not the stream's first non-empty line) nor the second
// (more content follows it before the stream ends) is ever a fence
// candidate that resolves to "strip", so the whole text streams through
// byte-for-byte unchanged.
func TestExtractJSONMiddleware_Stream_ProseEmbeddedFencesPassThrough(t *testing.T) {
	text := "Some prose\n```\ncode\n```\nmore prose"
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: text},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != text {
		t.Fatalf("got %q, want unchanged %q", got, text)
	}
}

// TestExtractJSONMiddleware_Stream_TruncatedNoClosingFence documents and
// pins the one deliberate divergence from Generate's whole-text rule: since
// Stream can't wait indefinitely to find out whether a closing fence will
// ever arrive, an opening fence is stripped unconditionally and
// immediately, even when the stream then ends with no closing fence at
// all — unlike Generate's stripFences, which requires both ends to match
// before stripping either (so it would leave this exact text untouched).
func TestExtractJSONMiddleware_Stream_TruncatedNoClosingFence(t *testing.T) {
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "```json\n{\"a\":1}"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)

	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got string
	for p := range sr.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			got += d.Text
		}
	}
	if err := sr.Err(); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got != `{"a":1}` {
		t.Fatalf("got %q, want %q (opening fence stripped, no closing fence to strip)", got, `{"a":1}`)
	}
}

func TestExtractJSONMiddleware_PassthroughFields(t *testing.T) {
	mock := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true}}
	wrapped := ExtractJSONMiddleware(mock)
	if wrapped.ModelID() != mock.ModelID() {
		t.Errorf("ModelID = %q", wrapped.ModelID())
	}
	if wrapped.ProviderName() != mock.ProviderName() {
		t.Errorf("ProviderName = %q", wrapped.ProviderName())
	}
	if wrapped.Capabilities() != mock.Caps {
		t.Errorf("Capabilities = %#v", wrapped.Capabilities())
	}
}

// ---------------------------------------------------------------------
// WrapImageModel
// ---------------------------------------------------------------------

type recordingImageModel struct {
	provider.ImageModel
	wrapped bool
}

func (m *recordingImageModel) ModelID() string { return "wrapped" }

func TestWrapImageModel(t *testing.T) {
	base := &aitest.MockImageModel{}
	got := WrapImageModel(base, func(m provider.ImageModel) provider.ImageModel {
		return &recordingImageModel{ImageModel: m, wrapped: true}
	})
	rec, ok := got.(*recordingImageModel)
	if !ok {
		t.Fatalf("got %T, want *recordingImageModel", got)
	}
	if !rec.wrapped {
		t.Fatal("wrap function's return value not used")
	}
	if got.ModelID() != "wrapped" {
		t.Errorf("ModelID() = %q", got.ModelID())
	}
}
