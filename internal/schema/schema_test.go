package schema

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

// --- []byte and time.Time wire-shape tests ---

type withBytesAndTime struct {
	Data      []byte    `json:"data"`
	CreatedAt time.Time `json:"createdAt"`
}

func TestForType_ByteSliceIsString(t *testing.T) {
	raw, err := For[withBytesAndTime]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	data := props["data"].(map[string]any)
	if data["type"] != "string" {
		t.Fatalf("[]byte schema type = %v, want string (encoding/json marshals []byte as base64 string)", data["type"])
	}
	if _, hasItems := data["items"]; hasItems {
		t.Fatalf("[]byte schema must not have array 'items': %v", data)
	}
}

func TestForType_TimeTimeIsStringDateTime(t *testing.T) {
	raw, err := For[withBytesAndTime]()
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	props := s["properties"].(map[string]any)
	createdAt := props["createdAt"].(map[string]any)
	if createdAt["type"] != "string" {
		t.Fatalf("time.Time schema type = %v, want string", createdAt["type"])
	}
	if createdAt["format"] != "date-time" {
		t.Fatalf("time.Time schema format = %v, want date-time", createdAt["format"])
	}
	if _, hasProps := createdAt["properties"]; hasProps {
		t.Fatalf("time.Time schema must not be expanded structurally: %v", createdAt)
	}
}

// TestForType_ByteSliceAndTimeRoundTrip verifies that a JSON value shaped
// per the generated schema (base64 string for []byte, RFC3339 string for
// time.Time) actually unmarshals into the struct -- the wire shape the
// schema advertises must match what encoding/json expects.
func TestForType_ByteSliceAndTimeRoundTrip(t *testing.T) {
	want := withBytesAndTime{
		Data:      []byte("hello world"),
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	marshaled, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	var s map[string]any
	if err := json.Unmarshal(marshaled, &s); err != nil {
		t.Fatal(err)
	}
	if _, ok := s["data"].(string); !ok {
		t.Fatalf("marshaled data = %v (%T), want string", s["data"], s["data"])
	}
	if _, ok := s["createdAt"].(string); !ok {
		t.Fatalf("marshaled createdAt = %v (%T), want string", s["createdAt"], s["createdAt"])
	}

	var got withBytesAndTime
	if err := json.Unmarshal(marshaled, &got); err != nil {
		t.Fatalf("unmarshal into struct: %v", err)
	}
	if string(got.Data) != string(want.Data) {
		t.Fatalf("Data = %q, want %q", got.Data, want.Data)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

// manyRequired has enough required fields that, prior to sorting "required",
// Go's randomized map iteration order would make repeated For[] calls
// produce different JSON bytes.
type manyRequired struct {
	Zebra   string `json:"zebra"`
	Alpha   string `json:"alpha"`
	Mike    string `json:"mike"`
	Golf    string `json:"golf"`
	Delta   string `json:"delta"`
	Charlie string `json:"charlie"`
	Kilo    string `json:"kilo"`
	Bravo   string `json:"bravo"`
}

// TestForType_RequiredOrderIsDeterministic asserts that (a) generating the
// schema for the same struct twice produces byte-identical output, and (b)
// the "required" array is in a specific, exact (sorted) order rather than
// whatever order the underlying map happened to iterate in.
func TestForType_RequiredOrderIsDeterministic(t *testing.T) {
	raw1, err := For[manyRequired]()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		raw2, err := For[manyRequired]()
		if err != nil {
			t.Fatal(err)
		}
		if string(raw1) != string(raw2) {
			t.Fatalf("schema bytes differ across runs:\n run 0: %s\n run %d: %s", raw1, i+1, raw2)
		}
	}

	var s map[string]any
	if err := json.Unmarshal(raw1, &s); err != nil {
		t.Fatal(err)
	}
	req := s["required"].([]any)
	want := []string{"alpha", "bravo", "charlie", "delta", "golf", "kilo", "mike", "zebra"}
	if len(req) != len(want) {
		t.Fatalf("required = %v, want %v", req, want)
	}
	for i, w := range want {
		if req[i] != w {
			t.Fatalf("required[%d] = %v, want %q (required = %v)", i, req[i], w, req)
		}
	}
}
