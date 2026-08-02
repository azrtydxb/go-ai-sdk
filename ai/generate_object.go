package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/internal/schema"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultSchemaName is used for the response schema / injected tool when
// GenerateObjectOpts.SchemaName is empty.
const defaultSchemaName = "output"

// GenerateObjectOpts configures a GenerateObject or StreamObject call.
type GenerateObjectOpts struct {
	Model             provider.LanguageModel // required
	System            string                 // optional; prepended as system message
	Prompt            string                 // exactly one of Prompt/Messages
	Messages          []provider.Message
	SchemaName        string // optional; default "output"
	SchemaDescription string
	MaxRetries        *int // default 2
	MaxTokens         *int
	Temperature       *float64
}

// GenerateObjectResult is the outcome of a GenerateObject call.
type GenerateObjectResult[T any] struct {
	Object       T
	RawText      string
	Usage        provider.Usage
	FinishReason provider.FinishReason
}

// buildObjectCall converts a GenerateObjectOpts into a provider.Call wired
// for schema-constrained object generation of T: when the model reports
// Capabilities().NativeJSON, via Call.ResponseFormat; otherwise via a
// single injected ToolDef with a forced ToolChoice. It returns the tool
// name used in tool mode, or "" in native JSON mode.
func buildObjectCall[T any](opts GenerateObjectOpts) (call provider.Call, toolName string, err error) {
	if opts.Model == nil {
		return provider.Call{}, "", errors.New("ai: GenerateObjectOpts.Model is required")
	}
	hasPrompt := opts.Prompt != ""
	hasMessages := len(opts.Messages) > 0
	if hasPrompt == hasMessages {
		return provider.Call{}, "", errors.New("ai: exactly one of Prompt or Messages must be set")
	}

	sch, err := schema.For[T]()
	if err != nil {
		return provider.Call{}, "", err
	}

	name := opts.SchemaName
	if name == "" {
		name = defaultSchemaName
	}

	var messages []provider.Message
	if opts.System != "" {
		messages = append(messages, provider.SystemText(opts.System))
	}
	if hasPrompt {
		messages = append(messages, provider.UserText(opts.Prompt))
	} else {
		messages = append(messages, opts.Messages...)
	}

	call = provider.Call{
		Messages:    messages,
		MaxTokens:   opts.MaxTokens,
		Temperature: opts.Temperature,
	}

	if opts.Model.Capabilities().NativeJSON {
		call.ResponseFormat = &provider.ResponseFormat{Type: "json", Schema: sch, Name: name}
		return call, "", nil
	}

	call.Tools = []provider.ToolDef{{Name: name, Description: opts.SchemaDescription, Schema: sch}}
	call.ToolChoice = &provider.ToolChoice{Mode: provider.ToolChoiceTool, ToolName: name}
	return call, name, nil
}

// stripFences removes a single surrounding markdown code fence
// ("```json\n...\n```" or "```\n...\n```") from s, if present. Input that
// isn't fenced is returned unchanged.
func stripFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || len(t) < 6 {
		return s
	}
	t = strings.TrimSuffix(t, "```")
	t = strings.TrimPrefix(t, "```")
	t = strings.TrimPrefix(t, "json")
	return strings.TrimSpace(t)
}

// decodeObject strips markdown fences from rawText (if present) and
// json.Unmarshals the result into a T. On failure it returns
// *NoObjectGeneratedError{RawText: rawText, Cause: err}.
func decodeObject[T any](rawText string) (T, error) {
	var obj T
	text := stripFences(rawText)
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return obj, &NoObjectGeneratedError{RawText: rawText, Cause: err}
	}
	return obj, nil
}

// GenerateObject calls opts.Model (through retry) once, and decodes its
// output into a T according to the schema for T, using native JSON mode or
// forced tool-call mode depending on opts.Model.Capabilities().NativeJSON.
func GenerateObject[T any](ctx context.Context, opts GenerateObjectOpts) (*GenerateObjectResult[T], error) {
	call, toolName, err := buildObjectCall[T](opts)
	if err != nil {
		return nil, err
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	resp, err := retry.Do(ctx, maxRetries, func() (*provider.Response, error) {
		return opts.Model.Generate(ctx, call)
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
		return nil, err
	}

	var rawText string
	if toolName != "" {
		calls := resp.ToolCalls()
		if len(calls) == 0 {
			return nil, &NoObjectGeneratedError{
				RawText: resp.Text(),
				Cause:   errors.New("model did not call the object tool"),
			}
		}
		rawText = string(calls[0].Args)
	} else {
		rawText = resp.Text()
	}

	obj, err := decodeObject[T](rawText)
	if err != nil {
		return nil, err
	}

	return &GenerateObjectResult[T]{
		Object:       obj,
		RawText:      rawText,
		Usage:        resp.Usage,
		FinishReason: resp.FinishReason,
	}, nil
}
