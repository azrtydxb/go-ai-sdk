// Package schema generates JSON Schema documents from Go struct types via
// reflection, following the subset of JSON Schema draft used by tool/object
// generation in the AI SDK.
package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	rawMessageType = reflect.TypeOf(json.RawMessage{})
	byteSliceType  = reflect.TypeOf([]byte(nil))
	timeType       = reflect.TypeOf(time.Time{})
)

// For reflects T (which must be a struct type) into a JSON Schema object.
func For[T any]() (json.RawMessage, error) {
	return ForType(reflect.TypeOf((*T)(nil)).Elem())
}

// ForType reflects t (which must be a struct type, or a pointer to one)
// into a JSON Schema object:
//
//	{"type":"object","properties":{...},"required":[...],"additionalProperties":false}
func ForType(t reflect.Type) (json.RawMessage, error) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == rawMessageType {
		return nil, fmt.Errorf("schema: %s is not a struct", t)
	}

	m, err := structSchema(t, map[reflect.Type]bool{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// fieldEntry holds a computed field schema plus enough bookkeeping to
// resolve name collisions between fields promoted from embedded structs at
// different depths, mirroring encoding/json's embedding rules.
type fieldEntry struct {
	schema   map[string]any
	required bool
	depth    int
}

// structSchema builds the object schema for a struct type, detecting cycles
// via the visiting set of types currently being expanded on the call stack.
func structSchema(t reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	if visiting[t] {
		return nil, fmt.Errorf("schema: cycle detected involving type %s", t)
	}
	visiting[t] = true
	defer delete(visiting, t)

	fields, err := collectFields(t, visiting, 0)
	if err != nil {
		return nil, err
	}

	properties := map[string]any{}
	required := []string{}
	for name, fe := range fields {
		properties[name] = fe.schema
		if fe.required {
			required = append(required, name)
		}
	}
	// Map iteration order is randomized; sort so the marshaled schema bytes
	// are deterministic across runs (needed for caching/golden comparisons).
	sort.Strings(required)

	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}, nil
}

// collectFields walks t's fields, promoting fields from anonymous
// (embedded) struct fields that carry no explicit json tag name into the
// result at depth+1 -- mirroring encoding/json's flattening semantics.
// Fields at a shallower depth win over deeper ones; a same-depth name
// collision drops the conflicting entries entirely, matching
// encoding/json's ambiguous-field behavior for the common one-level case.
func collectFields(t reflect.Type, visiting map[reflect.Type]bool, depth int) (map[string]fieldEntry, error) {
	result := map[string]fieldEntry{}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		name, omitempty, omitzero := parseJSONTag(f)
		if name == "-" {
			continue
		}

		if f.Anonymous && name == "" {
			// Pure embedding (no explicit json tag name): promote the
			// embedded struct's fields into this level, one depth deeper.
			ft := f.Type
			for ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && ft != rawMessageType {
				if visiting[ft] {
					return nil, fmt.Errorf("schema: cycle detected involving type %s", ft)
				}
				visiting[ft] = true
				sub, err := collectFields(ft, visiting, depth+1)
				delete(visiting, ft)
				if err != nil {
					return nil, err
				}
				for subName, fe := range sub {
					insertField(result, subName, fe)
				}
				continue
			}
			// Anonymous non-struct field: falls back to being a regular
			// field named after its type, same as encoding/json. Unexported
			// anonymous non-struct types cannot be marshaled by encoding/json.
			if f.PkgPath != "" {
				continue
			}
			name = f.Name
		} else {
			if f.PkgPath != "" {
				// unexported, non-anonymous field
				continue
			}
			if name == "" {
				name = f.Name
			}
		}

		fieldSchema, err := typeSchema(f.Type, visiting)
		if err != nil {
			return nil, fmt.Errorf("schema: field %s: %w", f.Name, err)
		}

		applyJSONSchemaTag(f, fieldSchema)

		req := f.Type.Kind() != reflect.Ptr && !omitempty && !omitzero
		insertField(result, name, fieldEntry{schema: fieldSchema, required: req, depth: depth})
	}

	return result, nil
}

// insertField adds fe under name, resolving collisions by depth: a
// shallower entry wins over a deeper one; entries at equal depth are
// ambiguous and are dropped, matching encoding/json's field resolution.
func insertField(result map[string]fieldEntry, name string, fe fieldEntry) {
	existing, ok := result[name]
	if !ok {
		result[name] = fe
		return
	}
	switch {
	case fe.depth < existing.depth:
		result[name] = fe
	case fe.depth == existing.depth:
		delete(result, name)
	default:
		// existing is shallower; keep it, ignore fe
	}
}

// typeSchema builds the schema fragment for an arbitrary Go type: scalars,
// slices/arrays, maps, nested structs, and pointers.
func typeSchema(t reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	if t == rawMessageType {
		// json.RawMessage represents an arbitrary JSON value: {} (any).
		return map[string]any{}, nil
	}

	// []byte marshals via encoding/json as a base64-encoded JSON string, not
	// as an array of integers, so the generic slice/array handling below
	// (which would produce {"type":"array","items":{"type":"integer"}} and
	// lie about the wire shape) must not see it.
	if t == byteSliceType {
		return map[string]any{
			"type":        "string",
			"description": "base64-encoded bytes (as produced by encoding/json for []byte)",
		}, nil
	}

	// time.Time implements json.Marshaler and marshals to an RFC 3339
	// string, not to the structural object the generic struct handling
	// below would otherwise produce. This special-case is intentionally
	// narrow: it does not attempt to detect json.Marshaler generically for
	// arbitrary types, since not every Marshaler marshals to a JSON string
	// (e.g. one could marshal to a number, array, or object), so a correct
	// generic rule isn't a simple "always treat as string." Types that
	// implement json.Marshaler other than time.Time will still be expanded
	// structurally by reflection, which is wrong for such types too --
	// callers with custom Marshaler types should special-case them the
	// same way if they hit this.
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}

	switch t.Kind() {
	case reflect.Ptr:
		return typeSchema(t.Elem(), visiting)

	case reflect.String:
		return map[string]any{"type": "string"}, nil

	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return map[string]any{"type": "integer"}, nil

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil

	case reflect.Slice, reflect.Array:
		items, err := typeSchema(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %s (only string keys are supported)", t.Key())
		}
		additional, err := typeSchema(t.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": additional}, nil

	case reflect.Struct:
		return structSchema(t, visiting)

	default:
		return nil, fmt.Errorf("unsupported kind %s", t.Kind())
	}
}

// parseJSONTag parses the `json` struct tag, returning the field name (empty
// if the tag was absent so the caller can fall back to the Go field name,
// or "-" if the field should be skipped) and whether omitempty/omitzero
// were set.
func parseJSONTag(f reflect.StructField) (name string, omitempty, omitzero bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return "", false, false
	}
	if tag == "-" {
		return "-", false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		switch opt {
		case "omitempty":
			omitempty = true
		case "omitzero":
			omitzero = true
		}
	}
	return name, omitempty, omitzero
}

// applyJSONSchemaTag parses the `jsonschema` struct tag (a comma-separated
// list of key=value pairs, where literal commas within a value may be
// escaped as `\,`) and merges description/enum entries into schema.
func applyJSONSchemaTag(f reflect.StructField, schema map[string]any) {
	tag := f.Tag.Get("jsonschema")
	if tag == "" {
		return
	}

	fieldType := f.Type
	for fieldType.Kind() == reflect.Ptr {
		fieldType = fieldType.Elem()
	}

	for _, tok := range splitEscaped(tag, ',') {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch key {
		case "description":
			schema["description"] = val
		case "enum":
			values := strings.Split(val, "|")
			enum := make([]any, len(values))
			for i, v := range values {
				enum[i] = coerce(v, fieldType)
			}
			schema["enum"] = enum
		}
	}
}

// splitEscaped splits s on sep, treating a backslash-escaped separator
// (`\<sep>`) as a literal character rather than a delimiter. The escaping
// backslash is removed from the result.
func splitEscaped(s string, sep rune) []string {
	var tokens []string
	var cur strings.Builder
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == sep:
			tokens = append(tokens, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	tokens = append(tokens, cur.String())
	return tokens
}

// coerce converts the string representation of an enum value to a Go value
// matching t's kind, so it marshals as the same JSON type as the field.
func coerce(s string, t reflect.Type) any {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
		return s
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			return n
		}
		return s
	case reflect.Float32, reflect.Float64:
		if n, err := strconv.ParseFloat(s, 64); err == nil {
			return n
		}
		return s
	case reflect.Bool:
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
		return s
	default:
		return s
	}
}
