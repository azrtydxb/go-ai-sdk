package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestConvertToolsStrict(t *testing.T) {
	out := convertTools([]provider.ToolDef{
		{Name: "strict_tool", Description: "d", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		{Name: "plain_tool", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if !out[0].Function.Strict {
		t.Errorf("strict_tool: Function.Strict = false, want true")
	}
	if out[1].Function.Strict {
		t.Errorf("plain_tool: Function.Strict = true, want false")
	}

	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"strict":true`) {
		t.Errorf("marshaled strict_tool = %s, want it to contain \"strict\":true", b)
	}

	b2, err := json.Marshal(out[1])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b2), `"strict"`) {
		t.Errorf("marshaled plain_tool = %s, want no \"strict\" field (omitempty)", b2)
	}
}
