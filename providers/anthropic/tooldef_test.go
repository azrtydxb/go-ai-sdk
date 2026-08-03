package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestConvertToolsInputExamples(t *testing.T) {
	examples := []json.RawMessage{
		json.RawMessage(`{"city":"Ghent"}`),
		json.RawMessage(`{"city":"Paris"}`),
	}
	out := convertTools([]provider.ToolDef{
		{
			Name:          "get_weather",
			Description:   "Get the weather",
			Schema:        json.RawMessage(`{"type":"object"}`),
			Strict:        true, // unsupported on anthropic's wire; must not appear
			InputExamples: examples,
		},
	})
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if len(out[0].InputExamples) != 2 {
		t.Fatalf("InputExamples = %v, want 2 entries", out[0].InputExamples)
	}

	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"input_examples"`) {
		t.Errorf("marshaled tool = %s, want it to contain \"input_examples\"", b)
	}
	if !strings.Contains(string(b), `"city":"Ghent"`) {
		t.Errorf("marshaled tool = %s, want it to contain example content", b)
	}
	if strings.Contains(string(b), `"strict"`) {
		t.Errorf("marshaled tool = %s, want no \"strict\" field (unsupported on anthropic)", b)
	}
}

func TestConvertToolsNoInputExamples(t *testing.T) {
	out := convertTools([]provider.ToolDef{
		{Name: "plain_tool", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), `"input_examples"`) {
		t.Errorf("marshaled tool = %s, want no \"input_examples\" field (omitempty)", b)
	}
}
