package codemode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// APIDoc renders tool signatures as language-flavored API documentation for
// the run_code tool description: one entry per tool with name, description,
// and its JSON schema rendered as a parameter listing.
//
// Each entry has the form:
//
//	functionName(args: {field: type, ...}) — description
//
// Fields come from the schema's "properties"/"required": optional fields
// (not listed in "required") are suffixed "?". Nested objects are rendered
// inline one level deep; anything deeper renders as the literal type
// "object". A schema with no "properties" (e.g. an MCP tool's opaque
// schema) renders as "(args: object)". Object property order is not
// meaningful in JSON (map keys), so properties are sorted alphabetically to
// keep the rendering deterministic across runs.
//
// language is accepted for future language-flavored rendering but does not
// currently change the output.
func APIDoc(language string, tools []ai.Tool) string {
	entries := make([]string, 0, len(tools))
	for _, t := range tools {
		entries = append(entries, apiDocEntry(t))
	}
	return strings.Join(entries, "\n")
}

// apiDocEntry renders a single tool's entry.
func apiDocEntry(t ai.Tool) string {
	var schemaMap map[string]any
	// Malformed/empty schema is treated the same as a properties-less
	// schema: render "(args: object)" rather than failing the whole doc.
	_ = json.Unmarshal(t.Schema(), &schemaMap)

	args := renderArgs(schemaMap)
	return fmt.Sprintf("%s(args: %s) — %s", t.Name(), args, t.Description())
}

// renderArgs renders the top-level "args" parameter type for a tool's
// schema: either the object's fields (expanded one level for nested
// objects) or the literal "object" when there are no properties to show.
func renderArgs(schemaMap map[string]any) string {
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return "object"
	}
	return renderObjectFields(schemaMap, true)
}

// renderObjectFields renders schemaMap's "properties" as a
// "{field: type, ...}" listing, sorted alphabetically by field name.
// allowExpand controls whether an object-typed field is itself expanded
// inline (one level) or collapsed to the literal type "object".
func renderObjectFields(schemaMap map[string]any, allowExpand bool) string {
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return "object"
	}

	required := map[string]bool{}
	if reqList, ok := schemaMap["required"].([]any); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	fields := make([]string, 0, len(names))
	for _, name := range names {
		propSchema, _ := properties[name].(map[string]any)
		ts := typeString(propSchema, allowExpand)
		suffix := "?"
		if required[name] {
			suffix = ""
		}
		fields = append(fields, fmt.Sprintf("%s%s: %s", name, suffix, ts))
	}

	return "{" + strings.Join(fields, ", ") + "}"
}

// typeString renders a single property schema's type. allowExpand controls
// whether an "object" typed property expands its own properties inline
// (used for the one level of nesting below the tool's top-level args) or
// collapses to the literal "object".
func typeString(propSchema map[string]any, allowExpand bool) string {
	if propSchema == nil {
		return "any"
	}

	t, _ := propSchema["type"].(string)
	switch t {
	case "array":
		items, _ := propSchema["items"].(map[string]any)
		return typeString(items, allowExpand) + "[]"
	case "object":
		if !allowExpand {
			return "object"
		}
		return renderObjectFields(propSchema, false)
	case "":
		return "any"
	default:
		return t
	}
}
