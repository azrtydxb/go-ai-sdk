package openaicompatible

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Guided is a vLLM guided-decoding constraint: the server restricts the
// generated tokens so the output is valid JSON for a schema, one of a fixed
// set of strings, or a match for a regular expression. Exactly one of JSON,
// Choice and Regex must be set. These are vLLM extensions to the chat
// completions request, not part of the OpenAI API; other servers ignore or
// reject them.
//
// vLLM before v0.12.0 took them as the top-level body fields guided_json,
// guided_choice and guided_regex (Legacy: true). From v0.12.0 those fields
// were removed in favor of one "structured_outputs" object with json,
// choice and regex members, which is what the zero value sends.
type Guided struct {
	JSON   json.RawMessage // a JSON Schema
	Choice []string
	Regex  string

	// Legacy sends the pre-v0.12.0 guided_json / guided_choice /
	// guided_regex fields instead of structured_outputs.
	Legacy bool
}

// GuidedOptions returns a ProviderOptions map that applies g to a chat
// call on this provider: set it as provider.Call.ProviderOptions (or
// ai.GenerateTextOpts.ProviderOptions). The fields are merged into the
// request body as extra top-level JSON, so no SDK request type changes.
func (p *Provider) GuidedOptions(g Guided) (map[string]any, error) {
	set := 0
	if len(g.JSON) > 0 {
		set++
		if !json.Valid(g.JSON) {
			return nil, errors.New("openaicompatible: Guided.JSON is not valid JSON")
		}
	}
	if len(g.Choice) > 0 {
		set++
	}
	if g.Regex != "" {
		set++
	}
	if set != 1 {
		return nil, fmt.Errorf("openaicompatible: exactly one of Guided.JSON, Choice and Regex must be set (got %d)", set)
	}

	var fields map[string]any
	switch {
	case len(g.JSON) > 0 && g.Legacy:
		fields = map[string]any{"guided_json": g.JSON}
	case len(g.Choice) > 0 && g.Legacy:
		fields = map[string]any{"guided_choice": g.Choice}
	case g.Regex != "" && g.Legacy:
		fields = map[string]any{"guided_regex": g.Regex}
	case len(g.JSON) > 0:
		fields = map[string]any{"structured_outputs": map[string]any{"json": g.JSON}}
	case len(g.Choice) > 0:
		fields = map[string]any{"structured_outputs": map[string]any{"choice": g.Choice}}
	default:
		fields = map[string]any{"structured_outputs": map[string]any{"regex": g.Regex}}
	}
	return map[string]any{p.name: fields}, nil
}
