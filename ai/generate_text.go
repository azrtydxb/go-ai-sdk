package ai

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultMaxRetries is used when GenerateTextOpts.MaxRetries is nil.
const defaultMaxRetries = 2

// ToolCallRecord records a tool call made by the model during a step.
type ToolCallRecord struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResultRecord records the outcome of executing a tool call.
type ToolResultRecord struct {
	ToolCallID string
	Name       string
	Result     any
	Err        error // tool execution error, recorded not raised (see Task 7)
}

// Step captures the result of a single model call within a GenerateText run.
type Step struct {
	Text         string
	ToolCalls    []ToolCallRecord
	ToolResults  []ToolResultRecord
	FinishReason provider.FinishReason
	Usage        provider.Usage
	Response     *provider.Response
}

// GenerateTextResult is the outcome of a GenerateText call.
type GenerateTextResult struct {
	Text         string // last step's text
	Steps        []Step
	ToolCalls    []ToolCallRecord   // last step's
	ToolResults  []ToolResultRecord // last step's
	FinishReason provider.FinishReason
	Usage        provider.Usage     // summed over steps
	Messages     []provider.Message // full final conversation incl. tool msgs
}

// GenerateText calls opts.Model once (through retry) and builds the result.
//
// This is the single-step implementation: it does not run the multi-step
// tool-calling loop (see Task 7).
func GenerateText(ctx context.Context, opts GenerateTextOpts) (*GenerateTextResult, error) {
	call, err := buildCall(opts)
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

	step := buildStep(resp)

	messages := make([]provider.Message, 0, len(call.Messages)+1)
	messages = append(messages, call.Messages...)
	messages = append(messages, provider.Message{Role: provider.RoleAssistant, Content: resp.Content})

	return &GenerateTextResult{
		Text:         step.Text,
		Steps:        []Step{step},
		ToolCalls:    step.ToolCalls,
		ToolResults:  step.ToolResults,
		FinishReason: step.FinishReason,
		Usage:        step.Usage,
		Messages:     messages,
	}, nil
}

// buildStep converts a provider.Response into a Step.
func buildStep(resp *provider.Response) Step {
	var toolCalls []ToolCallRecord
	for _, tc := range resp.ToolCalls() {
		toolCalls = append(toolCalls, ToolCallRecord{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		})
	}
	return Step{
		Text:         resp.Text(),
		ToolCalls:    toolCalls,
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
		Response:     resp,
	}
}

// WrapModel applies wrap to m, returning the wrapped model. It is a
// one-line hook for middleware that decorates a provider.LanguageModel
// (e.g. logging, caching, retries) before it is passed to GenerateText.
func WrapModel(m provider.LanguageModel, wrap func(provider.LanguageModel) provider.LanguageModel) provider.LanguageModel {
	return wrap(m)
}
