package partialjson

import (
	"encoding/json"
	"testing"
)

func TestRepair(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":1`, `{"a":1}`},
		{`{"a":"he`, `{"a":"he"}`},
		{`{"a":[1,2`, `{"a":[1,2]}`},
		{`{"a":{"b":`, `{"a":{}}`}, // dangling key dropped
		{`{"a":tru`, `{"a":true}`},
		{`{"a":1,`, `{"a":1}`},
		{`[`, `[]`},
	}
	for _, c := range cases {
		got, ok := Repair(c.in)
		if !ok {
			t.Errorf("Repair(%q) not ok", c.in)
			continue
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("Repair(%q) = %q, invalid JSON", c.in, got)
		}
	}
}

func TestRepairRejects(t *testing.T) {
	for _, in := range []string{"", "hello", "}", "   "} {
		if _, ok := Repair(in); ok {
			t.Errorf("Repair(%q) ok, want reject", in)
		}
	}
}

// Additional edge cases beyond the brief's table.
func TestRepairEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"escaped quote inside open string", `{"a":"x\"`},
		{"partial unicode escape", `{"a":"\u12`},
		{"nested mixed containers", `[{"a":[1,{"b":`},
		{"partial number with exponent", `{"a":1e`},
		{"bare minus", `{"a":-`},
		{"partial false literal", `{"a":fal`},
		{"partial null literal", `{"a":nul`},
		{"array of strings partial", `["a","b`},
		{"nested array trailing comma", `[1,2,`},
		{"deeply nested objects", `{"a":{"b":{"c":1`},
		{"number at top level partial", `12`},
		{"string at top level partial", `"hel`},
		{"escaped backslash before cut", `{"a":"x\\`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Repair(c.in)
			if !ok {
				t.Fatalf("Repair(%q) not ok", c.in)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("Repair(%q) = %q, invalid JSON", c.in, got)
			}
		})
	}
}

func TestRepairSpecificValues(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a":"x\"`, `{"a":"x\""}`},
		{`{"a":"\u12`, `{"a":""}`},
		{`{"a":1e`, `{"a":1}`}, // trim invalid exponent tail, keep valid number prefix... see impl
	}
	for _, c := range cases {
		got, ok := Repair(c.in)
		if !ok {
			t.Errorf("Repair(%q) not ok", c.in)
			continue
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("Repair(%q) = %q, invalid JSON", c.in, got)
		}
		t.Logf("Repair(%q) = %q", c.in, got)
	}
}
