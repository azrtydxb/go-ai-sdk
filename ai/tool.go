package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/azrtydxb/go-ai-sdk/internal/schema"
)

// Tool represents an executable tool that can be called by an LLM.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (any, error)

	// Strict reports whether the tool requests provider-enforced schema
	// conformance for its arguments (see provider.ToolDef.Strict). Most
	// tools return false.
	Strict() bool

	// InputExamples returns example argument payloads for the tool, each a
	// complete JSON object matching Schema (see provider.ToolDef.InputExamples).
	// Most tools return nil.
	InputExamples() []json.RawMessage

	// InputCallbacks returns the tool's input-streaming lifecycle hooks (see
	// ToolInputCallbacks and WithToolInputCallbacks). Most tools return a
	// zero-value ToolInputCallbacks (all fields nil); GenerateText and
	// StreamText nil-check every field before invoking it.
	InputCallbacks() ToolInputCallbacks
}

// ToolInputCallbacks are per-tool lifecycle hooks fired as a tool call's
// arguments become available, mirroring the Vercel AI SDK's v6
// onInputStart/onInputDelta/onInputAvailable. Attach via
// WithToolInputCallbacks.
//
// StreamText fires OnInputStart the first time an argument delta for a given
// toolCallID arrives, OnInputDelta for every argument delta thereafter
// (including that first one) with the raw args-JSON text fragment, and
// OnInputAvailable once the call's arguments are fully assembled — before
// that call is executed. GenerateText (no streaming, so no deltas exist)
// fires only OnInputAvailable, immediately before Execute. All three are
// nil-checked and invoked synchronously on the consuming goroutine, and none
// of them fire for the Output tool-mode synthetic call (see
// GenerateTextOpts.Output), since that call is never executed via Tool.
// Execute.
type ToolInputCallbacks struct {
	// OnInputStart fires once per toolCallID, when the first argument delta
	// for that call arrives. Stream-only: never fires from GenerateText.
	OnInputStart func(ctx context.Context, toolCallID string)

	// OnInputDelta fires once per argument delta (including the first),
	// carrying that fragment's raw args-JSON text. The concatenation of
	// every delta for a given toolCallID equals the call's fully assembled
	// arguments. Stream-only: never fires from GenerateText.
	OnInputDelta func(ctx context.Context, toolCallID string, delta string)

	// OnInputAvailable fires once per tool call, with its fully assembled
	// arguments, immediately before that call is executed. Fires from both
	// GenerateText and StreamText.
	OnInputAvailable func(ctx context.Context, toolCallID string, input json.RawMessage)
}

// tool is the concrete implementation of the Tool interface.
type tool struct {
	name           string
	description    string
	schema         json.RawMessage
	fn             any // We store this as any and use reflection to call it
	strict         bool
	inputExamples  []json.RawMessage
	inputCallbacks ToolInputCallbacks
}

// ToolOption configures optional NewTool behavior (strict mode, input
// examples).
type ToolOption func(*toolOptions)

type toolOptions struct {
	strict         bool
	inputExamples  []json.RawMessage
	inputCallbacks ToolInputCallbacks
}

// WithToolStrict marks the tool as requesting provider-enforced schema
// conformance (see provider.ToolDef.Strict).
func WithToolStrict() ToolOption {
	return func(o *toolOptions) {
		o.strict = true
	}
}

// WithToolInputExamples attaches example argument values to the tool.
// Each example is marshaled to JSON at construction time; it panics on
// marshal failure (treating this as a programmer error, similar to schema
// derivation).
func WithToolInputExamples[Args any](examples ...Args) ToolOption {
	return func(o *toolOptions) {
		for _, ex := range examples {
			b, err := json.Marshal(ex)
			if err != nil {
				panic(err)
			}
			o.inputExamples = append(o.inputExamples, json.RawMessage(b))
		}
	}
}

// WithToolInputCallbacks attaches per-tool input-streaming lifecycle hooks
// (see ToolInputCallbacks) to the resulting tool.
func WithToolInputCallbacks(cb ToolInputCallbacks) ToolOption {
	return func(o *toolOptions) {
		o.inputCallbacks = cb
	}
}

// NewTool creates a new Tool with a typed handler function.
// The Args type parameter is a struct that defines the tool's input schema.
// NewTool derives the schema from Args at construction; it panics on schema error
// (treating schema derivation as a programmer error, similar to regexp.MustCompile).
//
// When Execute is called, it unmarshals the args strictly (using json.Decoder with
// DisallowUnknownFields). On unmarshal failure, it returns *InvalidToolArgumentsError.
// If the function returns an error, Execute wraps it in *ToolExecutionError.
//
// Optional ToolOptions (WithToolStrict, WithToolInputExamples) configure the
// resulting tool's Strict/InputExamples values.
func NewTool[Args any](name, description string, fn func(context.Context, Args) (any, error), opts ...ToolOption) Tool {
	// Derive schema from Args type
	s, err := schema.For[Args]()
	if err != nil {
		panic(err)
	}

	var o toolOptions
	for _, opt := range opts {
		opt(&o)
	}

	return &tool{
		name:           name,
		description:    description,
		schema:         s,
		fn:             fn,
		strict:         o.strict,
		inputExamples:  o.inputExamples,
		inputCallbacks: o.inputCallbacks,
	}
}

func (t *tool) Name() string {
	return t.name
}

func (t *tool) Description() string {
	return t.description
}

func (t *tool) Schema() json.RawMessage {
	return t.schema
}

func (t *tool) Strict() bool {
	return t.strict
}

func (t *tool) InputExamples() []json.RawMessage {
	return t.inputExamples
}

func (t *tool) InputCallbacks() ToolInputCallbacks {
	return t.inputCallbacks
}

func (t *tool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	// Get the function as a reflect.Value to call it dynamically
	fnValue := reflect.ValueOf(t.fn)
	fnType := fnValue.Type()

	// The function signature is func(context.Context, Args) (any, error)
	// We need to unmarshal args into the Args type (second parameter)
	argType := fnType.In(1)

	// Create a new instance of the Args type
	argValue := reflect.New(argType)
	argInterface := argValue.Interface()

	// Unmarshal strictly with DisallowUnknownFields
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(argInterface); err != nil {
		return nil, &InvalidToolArgumentsError{
			ToolName: t.name,
			Args:     args,
			Cause:    err,
		}
	}

	// Check for trailing content after the JSON value
	if decoder.More() {
		return nil, &InvalidToolArgumentsError{
			ToolName: t.name,
			Args:     args,
			Cause:    fmt.Errorf("trailing content after JSON value"),
		}
	}

	// Call the function with context and unmarshaled args
	results := fnValue.Call([]reflect.Value{
		reflect.ValueOf(ctx),
		argValue.Elem(),
	})

	// Extract return values: (any, error)
	resultValue := results[0].Interface()
	errValue := results[1].Interface()

	// Check if there was an error
	if errValue != nil {
		err := errValue.(error)
		return nil, &ToolExecutionError{
			ToolName: t.name,
			Cause:    err,
		}
	}

	return resultValue, nil
}
