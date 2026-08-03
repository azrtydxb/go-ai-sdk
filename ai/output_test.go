package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type outBook struct {
	Title string `json:"title"`
	Pages int    `json:"pages"`
}

func TestOutputObjectNativeJSON(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"title":"Dune","pages":412}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	if err != nil {
		t.Fatal(err)
	}
	if m.Calls[0].ResponseFormat == nil || m.Calls[0].ResponseFormat.Type != "json" || m.Calls[0].ResponseFormat.Schema == nil {
		t.Fatalf("ResponseFormat = %+v", m.Calls[0].ResponseFormat)
	}
	book, err := OutputAs[outBook](res)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" || book.Pages != 412 {
		t.Fatalf("book = %+v", book)
	}
}

func TestOutputArrayDecodesElements(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"elements":[{"title":"Dune","pages":412},{"title":"Hyperion","pages":300}]}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "books", Output: OutputArray[outBook]()})
	if err != nil {
		t.Fatal(err)
	}

	// The schema request must carry the elements-array wrapper.
	var sch map[string]any
	if err := json.Unmarshal(m.Calls[0].ResponseFormat.Schema, &sch); err != nil {
		t.Fatal(err)
	}
	props, _ := sch["properties"].(map[string]any)
	if props == nil {
		t.Fatalf("schema has no properties: %s", m.Calls[0].ResponseFormat.Schema)
	}
	elements, ok := props["elements"].(map[string]any)
	if !ok || elements["type"] != "array" {
		t.Fatalf("schema.properties.elements = %#v", props["elements"])
	}
	if _, ok := elements["items"]; !ok {
		t.Fatal("schema.properties.elements.items missing")
	}

	books, err := OutputAs[[]outBook](res)
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 2 || books[0].Title != "Dune" || books[1].Title != "Hyperion" {
		t.Fatalf("books = %+v", books)
	}
}

func TestOutputChoiceDecodesEnum(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"result":"b"}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "pick", Output: OutputChoice("a", "b", "c")})
	if err != nil {
		t.Fatal(err)
	}

	var sch map[string]any
	if err := json.Unmarshal(m.Calls[0].ResponseFormat.Schema, &sch); err != nil {
		t.Fatal(err)
	}
	props, _ := sch["properties"].(map[string]any)
	result, ok := props["result"].(map[string]any)
	if !ok || result["type"] != "string" {
		t.Fatalf("schema.properties.result = %#v", props["result"])
	}
	enum, ok := result["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Fatalf("schema.properties.result.enum = %#v", result["enum"])
	}

	choice, err := OutputAs[string](res)
	if err != nil {
		t.Fatal(err)
	}
	if choice != "b" {
		t.Fatalf("choice = %q", choice)
	}
}

func TestOutputJSONNoSchema(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"anything":[1,2,3]}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Output: OutputJSON()})
	if err != nil {
		t.Fatal(err)
	}
	if m.Calls[0].ResponseFormat == nil || m.Calls[0].ResponseFormat.Type != "json" {
		t.Fatalf("ResponseFormat = %+v", m.Calls[0].ResponseFormat)
	}
	if m.Calls[0].ResponseFormat.Schema != nil {
		t.Fatalf("expected no schema in schemaless JSON mode, got %s", m.Calls[0].ResponseFormat.Schema)
	}
	val, err := OutputAs[map[string]any](res)
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := val["anything"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("val = %+v", val)
	}
}

func TestOutputToolModeFallback(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: defaultSchemaName, Args: []byte(`{"title":"Dune","pages":412}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("want exactly one model call, got %d", len(m.Calls))
	}
	call := m.Calls[0]
	if len(call.Tools) != 1 || call.ToolChoice == nil || call.ToolChoice.ToolName != call.Tools[0].Name {
		t.Fatalf("tool mode should inject schema tool with forced choice, call = %+v", call)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("want exactly one step, got %d", len(res.Steps))
	}
	if res.Steps[0].Text != `{"title":"Dune","pages":412}` {
		t.Fatalf("Steps[0].Text = %q, want the forced tool call args JSON", res.Steps[0].Text)
	}
	book, err := OutputAs[outBook](res)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" {
		t.Fatalf("book = %+v", book)
	}
}

func TestOutputToolModeFallbackSchemaDescription(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: defaultSchemaName, Args: []byte(`{"title":"Dune","pages":412}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:             m,
		Prompt:            "book",
		Output:            OutputObject[outBook](),
		SchemaDescription: "A book with title and page count",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Calls) != 1 {
		t.Fatalf("want exactly one model call, got %d", len(m.Calls))
	}
	call := m.Calls[0]
	if len(call.Tools) != 1 {
		t.Fatalf("want exactly one tool, got %d", len(call.Tools))
	}
	if call.Tools[0].Description != "A book with title and page count" {
		t.Fatalf("tool description = %q, want %q", call.Tools[0].Description, "A book with title and page count")
	}
	book, err := OutputAs[outBook](res)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" {
		t.Fatalf("book = %+v", book)
	}
}

func TestOutputNoNativeJSONWithToolsIsError(t *testing.T) {
	m := &aitest.MockModel{} // NativeJSON false
	_, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:  m,
		Prompt: "book",
		Output: OutputObject[outBook](),
		Tools:  []Tool{NewTool("t", "", func(ctx context.Context, a struct{}) (any, error) { return "ok", nil })},
	})
	if !errors.Is(err, ErrOutputRequiresJSONOrNoTools) {
		t.Fatalf("err = %v, want ErrOutputRequiresJSONOrNoTools", err)
	}
	if len(m.Calls) != 0 {
		t.Fatalf("want no model call, got %d", len(m.Calls))
	}
}

func TestOutputDecodeFailure(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: "I cannot comply."}},
			FinishReason: provider.FinishStop,
		}},
	}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) || noge.RawText != "I cannot comply." {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputAsMismatch(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"title":"Dune","pages":412}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OutputAs[string](res); err == nil {
		t.Fatal("want error on type mismatch")
	}

	m2 := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: "plain text"}},
			FinishReason: provider.FinishStop,
		}},
	}
	res2, err := GenerateText(t.Context(), GenerateTextOpts{Model: m2, Prompt: "book"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OutputAs[outBook](res2); err == nil {
		t.Fatal("want error when result has no Output")
	}
}

func TestOutputStripsFences(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: "```json\n{\"title\":\"Dune\",\"pages\":412}\n```"}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	if err != nil {
		t.Fatal(err)
	}
	book, err := OutputAs[outBook](res)
	if err != nil || book.Title != "Dune" {
		t.Fatalf("book = %+v, err = %v", book, err)
	}
}

func TestOutputMultiStepToolLoopThenDecode(t *testing.T) {
	type args struct{}
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{
			{
				Content:      []provider.ContentPart{provider.ToolCallPart{ID: "1", Name: "t", Args: []byte(`{}`)}},
				FinishReason: provider.FinishToolCalls,
			},
			{
				Content:      []provider.ContentPart{provider.TextPart{Text: `{"title":"Dune","pages":412}`}},
				FinishReason: provider.FinishStop,
			},
		},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model: m, Prompt: "book", MaxSteps: 2,
		Tools:  []Tool{NewTool("t", "", func(ctx context.Context, a args) (any, error) { return "ok", nil })},
		Output: OutputObject[outBook](),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(res.Steps))
	}
	if len(res.Steps[0].ToolResults) != 1 {
		t.Fatalf("want the real tool to have been executed in step 0, got %+v", res.Steps[0].ToolResults)
	}
	book, err := OutputAs[outBook](res)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" || book.Pages != 412 {
		t.Fatalf("book = %+v", book)
	}
}

// TestOutputToolModeFallbackTranscriptWellFormed pins finding 1: the
// tool-mode fallback must not leave Result.Messages ending with a dangling
// (unanswered) assistant tool call. It must append a synthetic RoleTool
// result message answering the forced call, and must not misreport
// FinishReason as tool-calls or leave the synthetic call in ToolCalls.
func TestOutputToolModeFallbackTranscriptWellFormed(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: defaultSchemaName, Args: []byte(`{"title":"Dune","pages":412}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	if err != nil {
		t.Fatal(err)
	}

	if res.FinishReason == provider.FinishToolCalls {
		t.Fatalf("FinishReason = %q, want anything but tool-calls", res.FinishReason)
	}
	if len(res.ToolCalls) != 0 {
		t.Fatalf("ToolCalls = %+v, want empty (synthetic call scrubbed)", res.ToolCalls)
	}
	if len(res.Steps) != 1 || len(res.Steps[0].ToolCalls) != 0 {
		t.Fatalf("Steps[0].ToolCalls = %+v, want empty", res.Steps[0].ToolCalls)
	}

	// The transcript must end with an assistant tool call answered by a
	// RoleTool message — never with a dangling assistant tool call.
	msgs := res.Messages
	if len(msgs) < 2 {
		t.Fatalf("want at least 2 messages (assistant tool call + tool result), got %d", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Role != provider.RoleTool {
		t.Fatalf("last message role = %q, want RoleTool answering the forced call", last.Role)
	}
	if len(last.Content) != 1 {
		t.Fatalf("last message content = %+v, want exactly one ToolResultPart", last.Content)
	}
	trp, ok := last.Content[0].(provider.ToolResultPart)
	if !ok {
		t.Fatalf("last message content[0] = %T, want provider.ToolResultPart", last.Content[0])
	}
	if trp.ToolCallID != "c1" {
		t.Fatalf("ToolResultPart.ToolCallID = %q, want %q", trp.ToolCallID, "c1")
	}

	assistant := msgs[len(msgs)-2]
	if assistant.Role != provider.RoleAssistant {
		t.Fatalf("second-to-last message role = %q, want RoleAssistant", assistant.Role)
	}
}

// TestOutputToolModeFallbackWrongToolNameErrors pins finding 4: if the model
// hallucinates a call to some other tool name instead of the injected
// output-schema tool, GenerateText must return a *NoObjectGeneratedError
// rather than silently decoding that call's args.
func TestOutputToolModeFallbackWrongToolNameErrors(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: "some_other_tool", Args: []byte(`{"foo":"bar"}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "book", Output: OutputObject[outBook]()})
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) {
		t.Fatalf("err = %v, want *NoObjectGeneratedError", err)
	}
}

// TestOutputChoiceRejectsOutOfEnumValue pins finding 2: OutputChoice.decode
// must reject a value outside the configured choice set (relevant on
// tool-mode providers, where the schema enum isn't enforced by the model).
func TestOutputChoiceRejectsOutOfEnumValue(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"result":"z"}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "pick", Output: OutputChoice("a", "b", "c")})
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) {
		t.Fatalf("err = %v, want *NoObjectGeneratedError for out-of-enum choice", err)
	}
}

// TestOutputChoiceAcceptsInEnumValue is the positive-path complement of
// TestOutputChoiceRejectsOutOfEnumValue.
func TestOutputChoiceAcceptsInEnumValue(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"result":"b"}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "pick", Output: OutputChoice("a", "b", "c")})
	if err != nil {
		t.Fatal(err)
	}
	choice, err := OutputAs[string](res)
	if err != nil || choice != "b" {
		t.Fatalf("choice = %q, err = %v", choice, err)
	}
}

// TestOutputChoiceEmptyChoicesIsError pins finding 3: OutputChoice() with
// zero choices must fail fast (up front, before any model call) rather than
// build an unsatisfiable {"enum":[]} schema.
func TestOutputChoiceEmptyChoicesIsError(t *testing.T) {
	m := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true}}
	_, err := GenerateText(t.Context(), GenerateTextOpts{Model: m, Prompt: "pick", Output: OutputChoice()})
	if err == nil {
		t.Fatal("want error for OutputChoice() with zero choices")
	}
	if len(m.Calls) != 0 {
		t.Fatalf("want no model call, got %d", len(m.Calls))
	}
}

// TestOutputSuspendedRunSkipsDecode pins finding 1: when the tool loop
// suspends on an approval (PendingApprovals non-empty), Output decoding must
// not run — the suspended step's Text is empty (or unrelated to the output
// schema), and decoding it would either fail (NoObjectGeneratedError,
// destroying the suspension) or, worse, silently "succeed" on garbage. A
// suspended run has no Output; it is decoded on the resumed run that
// actually completes.
func TestOutputSuspendedRunSkipsDecode(t *testing.T) {
	var executed bool
	tool := RequireApproval(approvalWeatherTool(&executed))
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{
			toolCallResponse("get_weather", "c1", `{"city":"Ghent"}`),
		},
	}
	var onErrorCalled bool
	res, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:    m,
		Prompt:   "book, checking weather first",
		Tools:    []Tool{tool},
		MaxSteps: 3,
		Output:   OutputObject[outBook](),
		OnError:  func(error) { onErrorCalled = true },
	})
	if err != nil {
		t.Fatalf("err = %v, want nil (suspension is not an error)", err)
	}
	if onErrorCalled {
		t.Fatal("OnError must not fire for a suspension")
	}
	if executed {
		t.Fatal("tool must not execute while pending")
	}
	if res.Output != nil {
		t.Fatalf("Output = %+v, want nil on a suspended run", res.Output)
	}
	if len(res.PendingApprovals) != 1 || res.PendingApprovals[0].Call.ID != "c1" {
		t.Fatalf("PendingApprovals = %+v", res.PendingApprovals)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Role != provider.RoleAssistant {
		t.Fatalf("last message role = %v, want assistant (round-trippable)", last.Role)
	}

	// Resume: approve the call, model then returns the structured output,
	// which must decode normally on the completed (non-suspended) run.
	m2 := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{
			{Content: []provider.ContentPart{provider.TextPart{Text: `{"title":"Dune","pages":412}`}}, FinishReason: provider.FinishStop},
		},
	}
	resumed, err := GenerateText(t.Context(), GenerateTextOpts{
		Model:     m2,
		Messages:  res.Messages,
		Tools:     []Tool{tool},
		MaxSteps:  3,
		Output:    OutputObject[outBook](),
		Approvals: []ApprovalDecision{{ToolCallID: "c1", Approved: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !executed {
		t.Fatal("tool should execute on resume")
	}
	book, err := OutputAs[outBook](resumed)
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Dune" || book.Pages != 412 {
		t.Fatalf("book = %+v", book)
	}
	if len(resumed.PendingApprovals) != 0 {
		t.Fatalf("PendingApprovals = %+v, want none on completed resume", resumed.PendingApprovals)
	}
}

func TestStreamTextWithOutputIsError(t *testing.T) {
	m := &aitest.MockModel{}
	_, err := StreamText(t.Context(), GenerateTextOpts{Model: m, Prompt: "x", Output: OutputObject[outBook]()})
	if !errors.Is(err, ErrOutputWithStreamText) {
		t.Fatalf("err = %v, want ErrOutputWithStreamText", err)
	}
	if len(m.Calls) != 0 {
		t.Fatalf("want no model call, got %d", len(m.Calls))
	}
}
