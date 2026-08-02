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

// --- Embedded/anonymous struct field tests ---

type Base struct {
	ID string `json:"id"`
}

type withExportedEmbed struct {
	Base
	Name string `json:"name"`
}

func TestForType_ExportedEmbedPromotesFields(t *testing.T) {
	raw, err := For[withExportedEmbed]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["Base"]; ok {
		t.Fatal("embedded type name must not appear as a nested property")
	}
	id, ok := props["id"].(map[string]any)
	if !ok || id["type"] != "string" {
		t.Fatalf("promoted id field = %v", props["id"])
	}
	if props["name"].(map[string]any)["type"] != "string" {
		t.Fatalf("name field = %v", props["name"])
	}

	req := s["required"].([]any)
	want := map[string]bool{"id": true, "name": true}
	if len(req) != len(want) {
		t.Fatalf("required = %v", req)
	}
	for _, r := range req {
		if !want[r.(string)] {
			t.Fatalf("unexpected required %v", r)
		}
	}
}

type base struct {
	ID     string `json:"id"`
	hidden string
}

type withUnexportedEmbed struct {
	base
	Name string `json:"name"`
}

func TestForType_UnexportedEmbedPromotesExportedFields(t *testing.T) {
	raw, err := For[withUnexportedEmbed]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if _, ok := props["base"]; ok {
		t.Fatal("embedded unexported type name must not appear as a nested property")
	}
	if props["id"] == nil {
		t.Fatal("expected promoted id field from unexported embedded type")
	}
	if _, ok := props["hidden"]; ok {
		t.Fatal("unexported field of embedded type must not be promoted")
	}
}

type withTaggedEmbed struct {
	Base `json:"base"`
	Name string `json:"name"`
}

func TestForType_TaggedEmbedStaysNested(t *testing.T) {
	raw, err := For[withTaggedEmbed]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	baseProp, ok := props["base"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested 'base' property, got %v", props)
	}
	if baseProp["type"] != "object" {
		t.Fatalf("base property = %v", baseProp)
	}
	if _, ok := props["id"]; ok {
		t.Fatal("tagged embed must not be flattened")
	}
}

type withPointerEmbed struct {
	*Base
	Name string `json:"name"`
}

func TestForType_PointerEmbedPromotesFields(t *testing.T) {
	raw, err := For[withPointerEmbed]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	if props["id"] == nil {
		t.Fatalf("expected promoted id field from embedded *Base, got %v", props)
	}
	if _, ok := props["Base"]; ok {
		t.Fatal("embedded pointer type name must not appear as a nested property")
	}
}

type embedCycleA struct {
	*embedCycleB
}
type embedCycleB struct {
	*embedCycleA
}

func TestForType_EmbeddedCycleDetection(t *testing.T) {
	if _, err := For[embedCycleA](); err == nil {
		t.Fatal("want error for cyclic embedding")
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
