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
