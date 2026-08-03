package ai

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/internal/schema"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ErrOutputWithStreamText is returned by StreamText when
// GenerateTextOpts.Output is set. Structured output modes are GenerateText-
// only for now; partial-output streaming is future work.
var ErrOutputWithStreamText = errors.New("ai: Output is not supported by StreamText (GenerateText only)")

// ErrOutputRequiresJSONOrNoTools is returned by GenerateText when
// GenerateTextOpts.Output has a schema, the model has no native JSON mode
// (Capabilities().NativeJSON is false), AND user Tools are set. Structured
// output without native JSON support requires a single injected tool call
// forced via ToolChoice, which cannot coexist with the caller's own tools.
var ErrOutputRequiresJSONOrNoTools = errors.New("ai: output: model has no native JSON mode and tools are in use; structured output modes require one or the other")

// Output selects a structured-output mode for GenerateText. Construct one
// with OutputObject, OutputArray, OutputChoice, or OutputJSON; the zero
// value (nil field) means plain text.
type Output interface {
	// schema returns the JSON schema to enforce, or nil for schemaless JSON
	// mode.
	schema() (name string, sch json.RawMessage, err error)
	// decode parses the model's final text into the mode's Go value.
	decode(rawText string) (any, error)
}

// objectOutput is the Output implementation for OutputObject[T].
type objectOutput[T any] struct{}

// OutputObject selects structured-output mode that decodes the model's
// response into a T, constrained by schema.For[T]().
func OutputObject[T any]() Output {
	return objectOutput[T]{}
}

func (objectOutput[T]) schema() (string, json.RawMessage, error) {
	sch, err := schema.For[T]()
	if err != nil {
		return "", nil, err
	}
	return defaultSchemaName, sch, nil
}

func (objectOutput[T]) decode(rawText string) (any, error) {
	return decodeObject[T](rawText)
}

// arrayElements wraps a []T under an "elements" key, matching the schema
// OutputArray requests: {"type":"object","properties":{"elements":
// {"type":"array","items":<schema.For[T]>}},"required":["elements"],
// "additionalProperties":false}.
type arrayElements[T any] struct {
	Elements []T `json:"elements"`
}

// arrayOutput is the Output implementation for OutputArray[T].
type arrayOutput[T any] struct{}

// OutputArray selects a structured-output mode that decodes the model's
// response into a []T. The requested schema wraps the per-element schema
// (schema.For[T]()) in an object with a single "elements" array property —
// most providers' schema-constrained JSON modes require a top-level object,
// not a bare array.
func OutputArray[T any]() Output {
	return arrayOutput[T]{}
}

func (arrayOutput[T]) schema() (string, json.RawMessage, error) {
	itemSchema, err := schema.For[T]()
	if err != nil {
		return "", nil, err
	}
	obj := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"elements": map[string]any{
				"type":  "array",
				"items": itemSchema,
			},
		},
		"required":             []string{"elements"},
		"additionalProperties": false,
	}
	sch, err := json.Marshal(obj)
	if err != nil {
		return "", nil, err
	}
	return defaultSchemaName, sch, nil
}

func (arrayOutput[T]) decode(rawText string) (any, error) {
	wrapper, err := decodeObject[arrayElements[T]](rawText)
	if err != nil {
		return nil, err
	}
	return wrapper.Elements, nil
}

// choiceResult wraps a single string under a "result" key, matching the
// schema OutputChoice requests.
type choiceResult struct {
	Result string `json:"result"`
}

// choiceOutput is the Output implementation for OutputChoice.
type choiceOutput struct {
	choices []string
}

// OutputChoice selects a structured-output mode that decodes the model's
// response into one of choices, enforced via a JSON schema enum. Calling it
// with zero choices is a configuration error: schema() returns an error
// (surfaced from GenerateText up front, before any model call) rather than
// building an unsatisfiable {"enum":[]} schema. The enum constraint is not
// necessarily enforced by the model itself (tool-mode providers don't
// validate arguments against the injected tool's schema), so decode also
// checks membership: a result outside choices returns a
// *NoObjectGeneratedError rather than returning it silently.
func OutputChoice(choices ...string) Output {
	return choiceOutput{choices: choices}
}

func (c choiceOutput) schema() (string, json.RawMessage, error) {
	if len(c.choices) == 0 {
		return "", nil, errors.New("ai: OutputChoice requires at least one choice")
	}
	enum := make([]any, len(c.choices))
	for i, s := range c.choices {
		enum[i] = s
	}
	obj := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result": map[string]any{
				"type": "string",
				"enum": enum,
			},
		},
		"required":             []string{"result"},
		"additionalProperties": false,
	}
	sch, err := json.Marshal(obj)
	if err != nil {
		return "", nil, err
	}
	return defaultSchemaName, sch, nil
}

func (c choiceOutput) decode(rawText string) (any, error) {
	wrapper, err := decodeObject[choiceResult](rawText)
	if err != nil {
		return nil, err
	}
	for _, choice := range c.choices {
		if wrapper.Result == choice {
			return wrapper.Result, nil
		}
	}
	// Tool-mode providers don't enforce the schema's enum constraint — the
	// model can return any string for the "result" field. Membership must
	// be checked here rather than trusted from the schema.
	return nil, &NoObjectGeneratedError{
		RawText: rawText,
		Cause:   fmt.Errorf("ai: OutputChoice: model returned %q, which is not one of the configured choices %v", wrapper.Result, c.choices),
	}
}

// jsonOutput is the Output implementation for OutputJSON.
type jsonOutput struct{}

// OutputJSON selects a schemaless structured-output mode that decodes the
// model's response as arbitrary JSON (map[string]any, []any, string,
// float64, bool, or nil, per encoding/json's default unmarshal-into-any
// rules). ResponseFormat.Type is set to "json" with no Schema, regardless of
// Model.Capabilities().NativeJSON — providers that can't honor a schemaless
// JSON response format just return text, which is decoded the same way.
func OutputJSON() Output {
	return jsonOutput{}
}

func (jsonOutput) schema() (string, json.RawMessage, error) {
	return "", nil, nil
}

func (jsonOutput) decode(rawText string) (any, error) {
	text := stripFences(rawText)
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return nil, &NoObjectGeneratedError{RawText: rawText, Cause: err}
	}
	return v, nil
}

// buildOutputCall extends buildCall with GenerateTextOpts.Output handling.
// It returns the tool name used for the tool-mode fallback ("" when Output
// is nil, uses native JSON ResponseFormat, or uses schemaless JSON mode).
func buildOutputCall(opts GenerateTextOpts) (call provider.Call, outputToolName string, err error) {
	call, err = buildCall(opts)
	if err != nil {
		return provider.Call{}, "", err
	}
	if opts.Output == nil {
		return call, "", nil
	}

	name, sch, err := opts.Output.schema()
	if err != nil {
		return provider.Call{}, "", err
	}

	if sch == nil {
		// Schemaless JSON mode (OutputJSON): always ResponseFormat{Type:
		// "json"}, regardless of NativeJSON.
		call.ResponseFormat = &provider.ResponseFormat{Type: "json"}
		return call, "", nil
	}

	if opts.Model.Capabilities().NativeJSON {
		call.ResponseFormat = &provider.ResponseFormat{Type: "json", Schema: sch, Name: name}
		return call, "", nil
	}

	if len(opts.Tools) > 0 {
		return provider.Call{}, "", ErrOutputRequiresJSONOrNoTools
	}

	call.Tools = []provider.ToolDef{{Name: name, Schema: sch}}
	call.ToolChoice = &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: name}
	return call, name, nil
}

// OutputAs extracts the decoded output as T from a GenerateTextResult
// produced with GenerateTextOpts.Output set. It returns a descriptive error
// (never a panic) when r has no decoded Output, or when its dynamic type
// doesn't match T.
func OutputAs[T any](r *GenerateTextResult) (T, error) {
	var zero T
	if r == nil || r.Output == nil {
		return zero, errors.New("ai: OutputAs: result has no decoded Output (set GenerateTextOpts.Output)")
	}
	v, ok := r.Output.(T)
	if !ok {
		return zero, fmt.Errorf("ai: OutputAs: output is %T, not %T", r.Output, zero)
	}
	return v, nil
}
