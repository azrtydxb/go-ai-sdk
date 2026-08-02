package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

type inner struct {
	N int `json:"n"`
}
type sample struct {
	Name    string   `json:"name" jsonschema:"description=the name"`
	Age     *int     `json:"age,omitempty"`
	Tags    []string `json:"tags"`
	Unit    string   `json:"unit" jsonschema:"enum=C|F"`
	Nested  inner    `json:"nested"`
	Skipped string   `json:"-"`
}

func TestForStruct(t *testing.T) {
	raw, err := For[sample]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	if s["type"] != "object" {
		t.Fatalf("type = %v", s["type"])
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["Skipped"]; ok {
		t.Fatal("json:\"-\" field must be skipped")
	}
	name := props["name"].(map[string]any)
	if name["type"] != "string" || name["description"] != "the name" {
		t.Fatalf("name = %v", name)
	}
	unit := props["unit"].(map[string]any)
	if enum := unit["enum"].([]any); len(enum) != 2 || enum[0] != "C" {
		t.Fatalf("enum = %v", unit["enum"])
	}
	tags := props["tags"].(map[string]any)
	if tags["type"] != "array" || tags["items"].(map[string]any)["type"] != "string" {
		t.Fatalf("tags = %v", tags)
	}
	if props["nested"].(map[string]any)["type"] != "object" {
		t.Fatal("nested not object")
	}

	req := s["required"].([]any)
	want := map[string]bool{"name": true, "tags": true, "unit": true, "nested": true}
	if len(req) != len(want) {
		t.Fatalf("required = %v", req)
	}
	for _, r := range req {
		if !want[r.(string)] {
			t.Fatalf("unexpected required %v", r)
		}
	}
	if s["additionalProperties"] != false {
		t.Fatal("additionalProperties must be false")
	}
}

func TestForRejectsNonStruct(t *testing.T) {
	if _, err := For[int](); err == nil {
		t.Fatal("want error for non-struct")
	}
}

// --- Additional covering tests ---

type withMap struct {
	Meta map[string]int `json:"meta"`
}

func TestForType_MapStringValue(t *testing.T) {
	raw, err := For[withMap]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	meta := props["meta"].(map[string]any)
	if meta["type"] != "object" {
		t.Fatalf("meta type = %v", meta["type"])
	}
	ap, ok := meta["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatalf("additionalProperties = %v", meta["additionalProperties"])
	}
	if ap["type"] != "integer" {
		t.Fatalf("additionalProperties type = %v", ap["type"])
	}
}

type withRaw struct {
	Payload json.RawMessage `json:"payload"`
}

func TestForType_RawMessageIsAny(t *testing.T) {
	raw, err := For[withRaw]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	payload := props["payload"].(map[string]any)
	if len(payload) != 0 {
		t.Fatalf("payload schema should be empty (any), got %v", payload)
	}
}

type cyclic struct {
	Self *cyclic `json:"self"`
}

func TestForType_CycleDetection(t *testing.T) {
	if _, err := For[cyclic](); err == nil {
		t.Fatal("want error for cyclic type")
	}
}

type withIntEnum struct {
	Level int `json:"level" jsonschema:"enum=1|2|3"`
}

func TestForType_EnumCoercedToInt(t *testing.T) {
	raw, err := For[withIntEnum]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	level := props["level"].(map[string]any)
	enum, ok := level["enum"].([]any)
	if !ok || len(enum) != 3 {
		t.Fatalf("enum = %v", level["enum"])
	}
	// JSON numbers decode as float64
	if enum[0] != float64(1) || enum[1] != float64(2) || enum[2] != float64(3) {
		t.Fatalf("enum values = %v", enum)
	}
}

type withEscapedComma struct {
	Name string `json:"name" jsonschema:"description=foo\\, bar,enum=a|b"`
}

func TestForType_EscapedCommaInDescription(t *testing.T) {
	raw, err := For[withEscapedComma]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	name := props["name"].(map[string]any)
	if name["description"] != "foo, bar" {
		t.Fatalf("description = %q", name["description"])
	}
	enum, ok := name["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "a" || enum[1] != "b" {
		t.Fatalf("enum = %v", name["enum"])
	}
}

func TestForType_NonStructError(t *testing.T) {
	// sanity: error message should mention the type is not usable, not panic
	_, err := For[[]string]()
	if err == nil {
		t.Fatal("want error for slice type")
	}
	if !strings.Contains(err.Error(), "struct") {
		t.Fatalf("error should mention struct requirement: %v", err)
	}
}
