package codemode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
)

// docTool is a minimal ai.Tool test double with a fixed name, description,
// and hand-written schema.
type docTool struct {
	name   string
	desc   string
	schema json.RawMessage
}

func (t *docTool) Name() string            { return t.name }
func (t *docTool) Description() string     { return t.desc }
func (t *docTool) Schema() json.RawMessage { return t.schema }
func (t *docTool) Execute(_ context.Context, _ json.RawMessage) (any, error) {
	panic("not used by these tests")
}
func (t *docTool) Strict() bool                     { return false }
func (t *docTool) InputExamples() []json.RawMessage { return nil }

func TestAPIDocGoldenOutput(t *testing.T) {
	search := &docTool{
		name: "search",
		desc: "Search something.",
		schema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string"},
				"limit": {"type": "integer"},
				"filters": {
					"type": "object",
					"properties": {
						"category": {"type": "string"},
						"tags": {"type": "array", "items": {"type": "string"}},
						"nested": {
							"type": "object",
							"properties": {"x": {"type": "string"}}
						}
					},
					"required": ["category"]
				}
			},
			"required": ["query"]
		}`),
	}
	lookup := &docTool{
		name:   "lookup",
		desc:   "Opaque lookup tool.",
		schema: json.RawMessage(`{"type": "object"}`),
	}

	want := "search(args: {filters?: {category: string, nested?: object, tags?: string[]}, limit?: integer, query: string}) — Search something.\n" +
		"lookup(args: object) — Opaque lookup tool."

	got := APIDoc("javascript", []ai.Tool{search, lookup})
	if got != want {
		t.Fatalf("APIDoc mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}
