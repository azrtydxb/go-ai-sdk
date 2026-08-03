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

func TestExtractJSONMiddleware_NilModelPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r != "ai: ExtractJSONMiddleware: nil model" {
			t.Fatalf("recover() = %v", r)
		}
	}()
	ExtractJSONMiddleware(nil)
	t.Fatal("did not panic")
}

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
	// No trailing newline: the "\n" immediately before the closing fence is
	// whitespace immediately preceding "```", which stripFences' trailing
	// TrimSpace also removes in Generate — see
	// TestExtractJSONMiddleware_GenerateStreamParity for the same shape
	// asserted against Generate directly.
	if got != `{"a":1}` {
		t.Fatalf("got %q, want %q", got, `{"a":1}`)
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
	// No trailing newline — see the matching comment in
	// TestExtractJSONMiddleware_Stream_StripsFenceInOneDelta.
	if got != `{"a":1}` {
		t.Fatalf("got %q, want %q", got, `{"a":1}`)
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

// streamText runs text through ExtractJSONMiddleware's Stream path as a
// single TextDelta and returns the concatenated stripped output. Used by
// the regression and parity tests below, where the shape of interest is
// the fence rule itself, not delta chunking.
func streamText(t *testing.T, text string) string {
	t.Helper()
	mock := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: text},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	wrapped := ExtractJSONMiddleware(mock)
	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
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
	return got
}

// streamTextSplit is streamText, but delivers text split across len(chunks)
// separate TextDeltas (chunks holds the split points, e.g. []int{2, 7} cuts
// text into three pieces) instead of a single delta — used to verify a
// fence marker (or tag, or trailing whitespace) split across arbitrary
// delta boundaries is still recognized correctly.
func streamTextSplit(t *testing.T, text string, cuts []int) string {
	t.Helper()
	var parts []provider.StreamPart
	prev := 0
	for _, c := range cuts {
		parts = append(parts, provider.TextDelta{Text: text[prev:c]})
		prev = c
	}
	parts = append(parts, provider.TextDelta{Text: text[prev:]})
	parts = append(parts, provider.FinishPart{Reason: provider.FinishStop})

	mock := &aitest.MockModel{Streams: [][]provider.StreamPart{parts}}
	wrapped := ExtractJSONMiddleware(mock)
	sr, err := wrapped.Stream(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
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
	return got
}

// generateText runs text through ExtractJSONMiddleware's Generate path and
// returns the stripped result, for direct comparison against streamText in
// the parity table below.
func generateText(t *testing.T, text string) string {
	t.Helper()
	mock := &aitest.MockModel{
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{provider.TextPart{Text: text}}},
		},
	}
	wrapped := ExtractJSONMiddleware(mock)
	resp, err := wrapped.Generate(context.Background(), provider.Call{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	tp, ok := resp.Content[0].(provider.TextPart)
	if !ok {
		t.Fatalf("Content[0] = %#v, want TextPart", resp.Content[0])
	}
	return tp.Text
}

// TestExtractJSONMiddleware_Stream_NoNewlineAtAllContentLoss is a regression
// test for a bug found in a scoped re-review of commit bc2ab7b: a fenced
// payload with NO newline anywhere ("```json{"a":1}```", single delta) used
// to stream as "" — the opening-fence handling discarded bytes unbounded
// until a newline, and none ever arrived, so the entire body was silently
// lost. Generate (via stripFences) correctly yields `{"a":1}` for the same
// text.
func TestExtractJSONMiddleware_Stream_NoNewlineAtAllContentLoss(t *testing.T) {
	got := streamText(t, "```json{\"a\":1}```")
	if got != `{"a":1}` {
		t.Fatalf("got %q, want %q", got, `{"a":1}`)
	}
}

// TestExtractJSONMiddleware_Stream_ClosingFenceGluedToContentLeakage is a
// regression test for the second bug found in that re-review: a closing
// fence glued directly to content with no preceding newline
// ("```json\n{\"a\":1}```") used to leak the literal "```" straight into the
// output, because closing-fence detection only ever looked at line starts.
func TestExtractJSONMiddleware_Stream_ClosingFenceGluedToContentLeakage(t *testing.T) {
	got := streamText(t, "```json\n{\"a\":1}```")
	if got != `{"a":1}` {
		t.Fatalf("got %q, want %q", got, `{"a":1}`)
	}
}

// TestExtractJSONMiddleware_Stream_NoNewlineAtAll_SplitAcrossDeltas covers
// the no-newline-at-all shape delivered across several TextDeltas, cutting
// through the opening fence marker, the "json" tag, and the closing fence
// marker at different points.
func TestExtractJSONMiddleware_Stream_NoNewlineAtAll_SplitAcrossDeltas(t *testing.T) {
	text := "```json{\"a\":1}```"
	cases := [][]int{
		{1, 2, 3, 7, len(text) - 2, len(text) - 1}, // cut inside opening ticks, tag, and closing ticks
		{3},             // cut right after the opening fence
		{7},             // cut right after "json"
		{len(text) - 3}, // cut right before the closing fence
	}
	for _, cuts := range cases {
		got := streamTextSplit(t, text, cuts)
		if got != `{"a":1}` {
			t.Errorf("cuts=%v: got %q, want %q", cuts, got, `{"a":1}`)
		}
	}
}

// TestExtractJSONMiddleware_Stream_ClosingFenceGlued_SplitAcrossDeltas
// covers the closing-fence-glued-to-content shape delivered across several
// TextDeltas.
func TestExtractJSONMiddleware_Stream_ClosingFenceGlued_SplitAcrossDeltas(t *testing.T) {
	text := "```json\n{\"a\":1}```"
	cases := [][]int{
		{1, 2, 3, 8, len(text) - 2, len(text) - 1},
		{8},             // cut right after the newline, start of body
		{len(text) - 3}, // cut right before the closing fence
	}
	for _, cuts := range cases {
		got := streamTextSplit(t, text, cuts)
		if got != `{"a":1}` {
			t.Errorf("cuts=%v: got %q, want %q", cuts, got, `{"a":1}`)
		}
	}
}

// TestExtractJSONMiddleware_GenerateStreamParity runs a table of fence
// shapes through BOTH Generate and Stream and asserts they produce
// IDENTICAL output, directly enforcing the "Stream mirrors Generate's rule
// as closely as streaming allows" contract rather than checking each path
// in isolation. Where Stream's one documented, deliberate divergence
// applies (a truncated stream with an opening fence but no closing one),
// that case is listed separately below, not in this table.
func TestExtractJSONMiddleware_GenerateStreamParity(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"no fence", `{"a":1}`},
		{"standard fenced with newlines", "```json\n{\"a\":1}\n```"},
		{"fenced, no tag", "```\n{\"a\":1}\n```"},
		{"no newline at all", "```json{\"a\":1}```"},
		{"closing fence glued to content", "```json\n{\"a\":1}```"},
		{"opening fence glued to tag and body, no newline before close, extra trailing ws", "```json{\"a\":1}   ```"},
		{"prose with embedded fences", "Some prose\n```\ncode\n```\nmore prose"},
		{"uppercase tag not recognized", "```JSON\n{\"a\":1}\n```"},
		{"leading whitespace before fence", "  ```json\n{\"a\":1}\n```"},
		{"fence with trailing whitespace before close", "```json\n{\"a\":1}\n   \n```"},
		{"unopened text ending in a fence marker", "Some prose\n```\ncode\n```"},
		{"unopened JSON ending in a fence marker", "{\"a\":1}\n```"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gen := generateText(t, tc.text)
			stream := streamText(t, tc.text)
			if gen != stream {
				t.Errorf("Generate = %q, Stream = %q (want identical)", gen, stream)
			}
		})
	}
}

// TestExtractJSONMiddleware_GenerateStreamParity_TruncatedDivergence pins
// the one deliberate, documented divergence between Generate and Stream
// (see ExtractJSONMiddleware's doc comment): a stream that's truncated
// before any closing fence appears still has its opening fence stripped,
// while Generate — which requires both ends to match before stripping
// either — leaves the whole text untouched.
func TestExtractJSONMiddleware_GenerateStreamParity_TruncatedDivergence(t *testing.T) {
	text := "```json\n{\"a\":1}"
	gen := generateText(t, text)
	stream := streamText(t, text)
	if gen != text {
		t.Errorf("Generate = %q, want unchanged %q (no closing fence, stripFences requires both ends)", gen, text)
	}
	if stream != `{"a":1}` {
		t.Errorf("Stream = %q, want %q (opening fence stripped even though truncated)", stream, `{"a":1}`)
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
