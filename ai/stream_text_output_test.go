package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type streamPerson struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// TestStreamTextOutputNativeJSON covers the native-JSON path: parts flow to
// the consumer unchanged, OnPartialOutput fires with successively more
// complete values, and Output() decodes the final text.
func TestStreamTextOutputNativeJSON(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"name":"bob","age":4`},
			provider.TextDelta{Text: `2}`},
			provider.FinishPart{Reason: provider.FinishStop, Usage: provider.Usage{TotalTokens: 7}},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputObject[streamPerson](),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Before any iteration there is no final text to decode: Output must
	// report nothing rather than a decode of the empty string.
	if v, err := s.Output(); v != nil || err != nil {
		t.Fatalf("Output() before iteration = (%v, %v), want (nil, nil)", v, err)
	}
	var text string
	var parts int
	for p := range s.Parts() {
		parts++
		if d, ok := p.(provider.TextDelta); ok {
			text += d.Text
		}
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if parts != 3 {
		t.Fatalf("yielded %d parts, want 3 (all parts flow unchanged)", parts)
	}
	if text != `{"name":"bob","age":42}` {
		t.Fatalf("streamed text = %q", text)
	}
	if len(partials) < 2 {
		t.Fatalf("OnPartialOutput fired %d times (%v), want >= 2", len(partials), partials)
	}
	first, ok := partials[0].(streamPerson)
	if !ok {
		t.Fatalf("partial[0] is %T, want streamPerson", partials[0])
	}
	if first.Name != "bob" || first.Age == 42 {
		t.Fatalf("partial[0] = %+v, want an intermediate value (name bob, age != 42)", first)
	}
	last := partials[len(partials)-1].(streamPerson)
	if last != (streamPerson{Name: "bob", Age: 42}) {
		t.Fatalf("last partial = %+v", last)
	}

	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if got, want := out.(streamPerson), (streamPerson{Name: "bob", Age: 42}); got != want {
		t.Fatalf("Output() = %+v, want %+v", got, want)
	}

	// The call must have carried the schema-constrained response format.
	calls := m.RecordedCalls()
	if len(calls) != 1 || calls[0].ResponseFormat == nil || calls[0].ResponseFormat.Schema == nil {
		t.Fatalf("call ResponseFormat = %+v, want schema-constrained JSON", calls[0].ResponseFormat)
	}
}

// TestStreamTextOutputToolMode covers the forced-tool fallback for models
// without native JSON: the forced call's parts are withheld from the
// consumer, its args become the step's text, and the transcript ends with
// the synthetic tool-result message.
func TestStreamTextOutputToolMode(t *testing.T) {
	args := `{"name":"bob","age":42}`
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.ToolCallDelta{ID: "c1", Name: defaultSchemaName, ArgsDelta: `{"name":"bob",`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"age":42}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: defaultSchemaName, Args: []byte(args)}},
			provider.FinishPart{Reason: provider.FinishToolCalls, Usage: provider.Usage{TotalTokens: 7}},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputObject[streamPerson](),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []provider.StreamPart
	for p := range s.Parts() {
		got = append(got, p)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	for _, p := range got {
		switch p.(type) {
		case provider.ToolCallDelta, provider.ToolCallEnd:
			t.Fatalf("forced output tool part leaked to consumer: %T", p)
		}
	}
	if len(got) != 1 {
		t.Fatalf("yielded %d parts (%v), want only the FinishPart", len(got), got)
	}
	if s.Text() != args {
		t.Fatalf("Text() = %q, want %q", s.Text(), args)
	}
	if s.FinishReason() != provider.FinishStop {
		t.Fatalf("FinishReason() = %v, want stop", s.FinishReason())
	}
	if steps := s.Steps(); len(steps) != 1 || len(steps[0].ToolCalls) != 0 {
		t.Fatalf("steps = %+v, want 1 step with scrubbed ToolCalls", steps)
	}
	if len(partials) == 0 {
		t.Fatal("OnPartialOutput never fired in tool mode")
	}
	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if got, want := out.(streamPerson), (streamPerson{Name: "bob", Age: 42}); got != want {
		t.Fatalf("Output() = %+v, want %+v", got, want)
	}

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleTool {
		t.Fatalf("last message role = %v, want tool", last.Role)
	}
	tr, ok := last.Content[0].(provider.ToolResultPart)
	if !ok || tr.ToolCallID != "c1" || tr.Name != defaultSchemaName || tr.Result != args {
		t.Fatalf("synthetic tool result = %+v", last.Content[0])
	}
}

// TestStreamTextOutputWrongToolName covers a model that calls some other
// tool than the injected output tool in forced mode.
func TestStreamTextOutputWrongToolName(t *testing.T) {
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: "other", Args: []byte(`{}`)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		}},
	}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "who", Output: OutputObject[streamPerson]()})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	var noObj *NoObjectGeneratedError
	if !errors.As(s.Err(), &noObj) {
		t.Fatalf("Err() = %v (%T), want *NoObjectGeneratedError", s.Err(), s.Err())
	}
	out, oerr := s.Output()
	if out != nil {
		t.Fatalf("Output() value = %v, want nil", out)
	}
	if !errors.Is(oerr, s.Err()) {
		t.Fatalf("Output() err = %v, want the same error as Err() = %v", oerr, s.Err())
	}
}

// TestStreamTextOutputDecodeFailure covers non-JSON final text: the parts
// still flow and the stream itself is not errored — the failure surfaces
// only from Output().
func TestStreamTextOutputDecodeFailure(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "sorry, I can't do that"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	s, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "who", Output: OutputObject[streamPerson]()})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for p := range s.Parts() {
		if d, ok := p.(provider.TextDelta); ok {
			text += d.Text
		}
	}
	if s.Err() != nil {
		t.Fatalf("Err() = %v, want nil (decode failure is not a stream error)", s.Err())
	}
	if text != "sorry, I can't do that" {
		t.Fatalf("text = %q", text)
	}
	out, oerr := s.Output()
	if out != nil {
		t.Fatalf("Output() value = %v, want nil", out)
	}
	var noObj *NoObjectGeneratedError
	if !errors.As(oerr, &noObj) {
		t.Fatalf("Output() err = %v (%T), want *NoObjectGeneratedError", oerr, oerr)
	}
}

// TestStreamTextOutputChoiceNoPartials covers OutputChoice: choices are
// atomic, so no partial ever fires, but the final value decodes.
func TestStreamTextOutputChoiceNoPartials(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"result":`},
			provider.TextDelta{Text: `"yes"}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	fired := 0
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "yes or no",
		Output:          OutputChoice("yes", "no"),
		OnPartialOutput: func(any) { fired++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if fired != 0 {
		t.Fatalf("OnPartialOutput fired %d times, want 0 for OutputChoice", fired)
	}
	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if out != "yes" {
		t.Fatalf("Output() = %v, want %q", out, "yes")
	}
}

// TestStreamTextOutputToolModeSecondCallIgnored covers a model that emits
// TWO calls to the injected output tool. Output() decodes the FIRST call
// (findToolCallByName is first-match-wins), so the partial-output tap must
// track that same call: the second call's args must never be reported, and
// the last partial must equal Output().
func TestStreamTextOutputToolModeSecondCallIgnored(t *testing.T) {
	first := `{"name":"bob","age":42}`
	second := `{"name":"eve","age":7}`
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.ToolCallDelta{ID: "c1", Name: defaultSchemaName, ArgsDelta: `{"name":"bob",`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"age":42}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: defaultSchemaName, Args: []byte(first)}},
			provider.ToolCallDelta{ID: "c2", Name: defaultSchemaName, ArgsDelta: second},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c2", Name: defaultSchemaName, Args: []byte(second)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputObject[streamPerson](),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(partials) == 0 {
		t.Fatal("OnPartialOutput never fired")
	}
	for i, p := range partials {
		if p.(streamPerson).Name == "eve" {
			t.Fatalf("partial[%d] = %+v came from the SECOND output tool call", i, p)
		}
	}
	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if got, want := out.(streamPerson), (streamPerson{Name: "bob", Age: 42}); got != want {
		t.Fatalf("Output() = %+v, want %+v (first call wins)", got, want)
	}
	if last := partials[len(partials)-1]; last != out {
		t.Fatalf("last partial %+v != Output() %+v", last, out)
	}
}

// TestStreamTextOutputFencedPartials covers a model that wraps its JSON in a
// markdown code fence: partials must fire despite the fence prefix.
func TestStreamTextOutputFencedPartials(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "```json\n{\"name\":\"bob\""},
			provider.TextDelta{Text: ",\"age\":42}\n```"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputObject[streamPerson](),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(partials) < 2 {
		t.Fatalf("fenced stream fired %d partials (%v), want >= 2", len(partials), partials)
	}
	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if got, want := out.(streamPerson), (streamPerson{Name: "bob", Age: 42}); got != want {
		t.Fatalf("Output() = %+v, want %+v", got, want)
	}
	if last := partials[len(partials)-1]; last != out {
		t.Fatalf("last partial %+v != Output() %+v", last, out)
	}
}

// TestStreamTextOutputFencedPartialsJSONMode covers the schemaless JSON mode
// on the same fenced stream: jsonOutput.decodePartial must strip the fence
// PREFIX (a whole-document fence strip never matches a prefix that has no
// closing fence yet).
func TestStreamTextOutputFencedPartialsJSONMode(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: "```json\n{\"name\":\"bob\""},
			provider.TextDelta{Text: ",\"age\":42}\n```"},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputJSON(),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(partials) < 2 {
		t.Fatalf("fenced stream fired %d partials (%v), want >= 2", len(partials), partials)
	}
	last, ok := partials[len(partials)-1].(map[string]any)
	if !ok || last["name"] != "bob" {
		t.Fatalf("last partial = %v", partials[len(partials)-1])
	}
}

// TestStreamTextOutputToolModeLateCallID covers providers (openai-compatible,
// mistral) that emit a tool call whose ID is empty until a later wire chunk
// carries it: the tap must adopt the real ID rather than freezing on "" and
// dropping every subsequent delta.
func TestStreamTextOutputToolModeLateCallID(t *testing.T) {
	args := `{"name":"bob","age":42}`
	m := &aitest.MockModel{
		Streams: [][]provider.StreamPart{{
			provider.ToolCallDelta{ID: "", Name: defaultSchemaName, ArgsDelta: `{"name":`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"bob",`},
			provider.ToolCallDelta{ID: "c1", ArgsDelta: `"age":42}`},
			provider.ToolCallEnd{Call: provider.ToolCallPart{ID: "c1", Name: defaultSchemaName, Args: []byte(args)}},
			provider.FinishPart{Reason: provider.FinishToolCalls},
		}},
	}
	var partials []any
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "who",
		Output:          OutputObject[streamPerson](),
		OnPartialOutput: func(v any) { partials = append(partials, v) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Parts() {
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if len(partials) < 2 {
		t.Fatalf("fired %d partials (%v), want >= 2 (the late-ID deltas must keep feeding the tap)", len(partials), partials)
	}
	out, oerr := s.Output()
	if oerr != nil {
		t.Fatal(oerr)
	}
	if last := partials[len(partials)-1]; last != out {
		t.Fatalf("last partial %+v != Output() %+v", last, out)
	}
}

// TestStreamTextOutputChoiceSkipsTracker covers the atomic-mode fast path:
// OutputChoice never yields partials, so the tap must not accumulate or
// repair anything at all.
func TestStreamTextOutputChoiceSkipsTracker(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Streams: [][]provider.StreamPart{{
			provider.TextDelta{Text: `{"result":`},
			provider.TextDelta{Text: `"yes"}`},
			provider.FinishPart{Reason: provider.FinishStop},
		}},
	}
	fired := 0
	s, err := StreamText(t.Context(), GenerateTextOpts{
		Model:           m,
		Prompt:          "yes or no",
		Output:          OutputChoice("yes", "no"),
		OnPartialOutput: func(any) { fired++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.outputAtomic {
		t.Fatal("outputAtomic = false for OutputChoice, want true")
	}
	for range s.Parts() {
	}
	if fired != 0 {
		t.Fatalf("OnPartialOutput fired %d times, want 0", fired)
	}
	if got := s.outputTracker.text(); got != "" {
		t.Fatalf("tracker accumulated %q, want nothing (atomic mode skips it)", got)
	}
	out, oerr := s.Output()
	if oerr != nil || out != "yes" {
		t.Fatalf("Output() = (%v, %v), want (\"yes\", nil)", out, oerr)
	}
}
