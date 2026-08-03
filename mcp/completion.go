package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// CompletionRef identifies what argument completion is being requested for:
// either a prompt (Type "ref/prompt", Name set) or a resource template
// (Type "ref/resource", URI set).
type CompletionRef struct {
	Type string // "ref/prompt" or "ref/resource"
	Name string // prompt name (ref/prompt)
	URI  string // resource template URI (ref/resource)
}

// Completion is the server's suggested completions for one argument.
type Completion struct {
	Values  []string
	Total   int // server's total count when provided
	HasMore bool
}

type completionRefWire struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	URI  string `json:"uri,omitempty"`
}

type completionArgumentWire struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type completionCompleteParams struct {
	Ref      completionRefWire      `json:"ref"`
	Argument completionArgumentWire `json:"argument"`
}

type completionResultWire struct {
	Values  []string `json:"values"`
	Total   int      `json:"total,omitempty"`
	HasMore bool     `json:"hasMore,omitempty"`
}

type completionCompleteResult struct {
	Completion completionResultWire `json:"completion"`
}

// Complete issues "completion/complete" to request argument autocompletion
// suggestions for argName/argValue against ref (a prompt or resource
// template). It returns a *CapabilityError without sending any request if
// the server's "initialize" response did not advertise the "completions"
// capability.
func (c *Client) Complete(ctx context.Context, ref CompletionRef, argName, argValue string) (*Completion, error) {
	if !c.hasCapability("completions") {
		return nil, &CapabilityError{Capability: "completions"}
	}
	params := completionCompleteParams{
		Ref:      completionRefWire{Type: ref.Type, Name: ref.Name, URI: ref.URI},
		Argument: completionArgumentWire{Name: argName, Value: argValue},
	}
	raw, err := c.call(ctx, "completion/complete", params)
	if err != nil {
		return nil, fmt.Errorf("mcp: completion/complete: %w", err)
	}
	var res completionCompleteResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("mcp: decode completion/complete result: %w", err)
	}
	return &Completion{
		Values:  res.Completion.Values,
		Total:   res.Completion.Total,
		HasMore: res.Completion.HasMore,
	}, nil
}
