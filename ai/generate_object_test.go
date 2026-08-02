package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type forecast struct {
	City string `json:"city"`
	Temp int    `json:"temp"`
}

func TestGenerateObjectNativeJSON(t *testing.T) {
	m := &aitest.MockModel{
		Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: `{"city":"Ghent","temp":21}`}},
			FinishReason: provider.FinishStop,
		}},
	}
	res, err := GenerateObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "forecast"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Object.City != "Ghent" || res.Object.Temp != 21 {
		t.Fatalf("obj = %+v", res.Object)
	}
	if m.Calls[0].ResponseFormat == nil || m.Calls[0].ResponseFormat.Type != "json" {
		t.Fatal("native JSON mode should set ResponseFormat")
	}
}

func TestGenerateObjectToolMode(t *testing.T) {
	m := &aitest.MockModel{ // NativeJSON false
		Responses: []*provider.Response{{
			Content: []provider.ContentPart{provider.ToolCallPart{
				ID: "c1", Name: "output", Args: []byte(`{"city":"Ghent","temp":21}`)}},
			FinishReason: provider.FinishToolCalls,
		}},
	}
	res, err := GenerateObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "forecast"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Object.Temp != 21 {
		t.Fatalf("obj = %+v", res.Object)
	}
	call := m.Calls[0]
	if len(call.Tools) != 1 || call.ToolChoice == nil || call.ToolChoice.ToolName != "output" {
		t.Fatal("tool mode should inject schema tool with forced choice")
	}
}

func TestGenerateObjectStripsFences(t *testing.T) {
	m := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: "```json\n{\"city\":\"G\",\"temp\":1}\n```"}},
			FinishReason: provider.FinishStop}}}
	res, err := GenerateObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	if err != nil || res.Object.Temp != 1 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestGenerateObjectFailure(t *testing.T) {
	m := &aitest.MockModel{Caps: provider.Capabilities{NativeJSON: true},
		Responses: []*provider.Response{{
			Content:      []provider.ContentPart{provider.TextPart{Text: "I cannot."}},
			FinishReason: provider.FinishStop}}}
	_, err := GenerateObject[forecast](t.Context(), GenerateObjectOpts{Model: m, Prompt: "x"})
	var noge *NoObjectGeneratedError
	if !errors.As(err, &noge) || noge.RawText != "I cannot." {
		t.Fatalf("err = %v", err)
	}
}
