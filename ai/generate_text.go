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

// defaultMaxStepsWithStopWhen is the hard cap on steps used when MaxSteps is
// unset (0) but StopWhen is set. See GenerateTextOpts.MaxSteps.
const defaultMaxStepsWithStopWhen = 16

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

// StepPlan is the input/output of GenerateTextOpts.PrepareStep: the Call
// about to be sent for a step, and the LanguageModel that will send it.
type StepPlan struct {
	Call provider.Call
	// Model is the model that will make this step's call. On the way in,
	// it is always the model currently active for the loop (opts.Model, or
	// whatever a prior PrepareStep call swapped to). On the way out, a nil
	// Model means keep the current model; a non-nil Model swaps to it for
	// this step and every step after, until PrepareStep swaps again — see
	// GenerateTextOpts.PrepareStep for why the swap persists.
	Model provider.LanguageModel
}

// Step captures the result of a single model call within a GenerateText run.
type Step struct {
	Text          string
	ReasoningText string                // concatenated ReasoningParts of this step's Response
	Sources       []provider.SourcePart // this step's Response.SourceParts()
	ToolCalls     []ToolCallRecord
	ToolResults   []ToolResultRecord
	FinishReason  provider.FinishReason
	Usage         provider.Usage
	Response      *provider.Response
}

// GenerateTextResult is the outcome of a GenerateText call.
type GenerateTextResult struct {
	Text          string                // last step's text
	ReasoningText string                // last step's reasoning text
	Sources       []provider.SourcePart // last step's sources
	Steps         []Step
	ToolCalls     []ToolCallRecord   // last step's
	ToolResults   []ToolResultRecord // last step's
	FinishReason  provider.FinishReason
	Usage         provider.Usage     // summed over steps
	Messages      []provider.Message // full final conversation incl. tool msgs
}

// GenerateText calls opts.Model (through retry), running a multi-step
// tool-calling loop when the model requests tool calls.
//
// After a response whose FinishReason is tool-calls (or that contains
// ToolCallParts), if an unknown tool is requested, GenerateText returns a
// *NoSuchToolError. Otherwise it executes all tool calls sequentially in
// response order, appends the assistant message and a single RoleTool
// message with all tool results, and calls the model again. This repeats
// while tool calls occur and len(Steps) < MaxSteps (default 1). Usage is
// summed across steps.
func GenerateText(ctx context.Context, opts GenerateTextOpts) (*GenerateTextResult, error) {
	call, err := buildCall(opts)
	if err != nil {
		return nil, err
	}

	maxRetries := defaultMaxRetries
	if opts.MaxRetries != nil {
		maxRetries = *opts.MaxRetries
	}

	maxSteps := 1
	if opts.MaxSteps > 0 {
		maxSteps = opts.MaxSteps
	} else if opts.StopWhen != nil {
		maxSteps = defaultMaxStepsWithStopWhen
	}

	messages := append([]provider.Message(nil), call.Messages...)

	var steps []Step
	var totalUsage provider.Usage
	model := opts.Model

	for {
		stepIndex := len(steps)
		stepCall := call
		stepCall.Messages = messages
		if opts.PrepareStep != nil {
			if plan, ok := opts.PrepareStep(stepIndex, StepPlan{Call: stepCall, Model: model}); ok {
				stepCall = plan.Call
				if plan.Model != nil {
					model = plan.Model
				}
			}
		}

		resp, err := retry.Do(ctx, maxRetries, func() (*provider.Response, error) {
			return model.Generate(ctx, stepCall)
		})
		if err != nil {
			var exhausted *retry.ExhaustedError
			if errors.As(err, &exhausted) {
				return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
			}
			return nil, err
		}

		step := buildStep(resp)
		totalUsage.InputTokens += resp.Usage.InputTokens
		totalUsage.OutputTokens += resp.Usage.OutputTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens
		totalUsage.CachedInputTokens += resp.Usage.CachedInputTokens
		totalUsage.ReasoningTokens += resp.Usage.ReasoningTokens

		messages = append(messages, provider.Message{Role: provider.RoleAssistant, Content: resp.Content})

		toolCalls := resp.ToolCalls()
		hasToolCalls := len(toolCalls) > 0

		if hasToolCalls {
			results, err := runToolCalls(ctx, opts.Tools, toolCalls)
			if err != nil {
				return nil, err
			}
			step.ToolResults = results

			resultParts := make([]provider.ContentPart, 0, len(results))
			for _, r := range results {
				resultParts = append(resultParts, provider.ToolResultPart{
					ToolCallID: r.ToolCallID,
					Name:       r.Name,
					Result:     toolResultValue(r),
					IsError:    r.Err != nil,
				})
			}
			messages = append(messages, provider.Message{Role: provider.RoleTool, Content: resultParts})
		}

		steps = append(steps, step)

		if opts.OnStepFinish != nil {
			opts.OnStepFinish(step)
		}

		if !hasToolCalls {
			break
		}
		if len(steps) >= maxSteps {
			break
		}
		if opts.StopWhen != nil && opts.StopWhen(steps) {
			break
		}
	}

	last := steps[len(steps)-1]

	return &GenerateTextResult{
		Text:          last.Text,
		ReasoningText: last.ReasoningText,
		Sources:       last.Sources,
		Steps:         steps,
		ToolCalls:     last.ToolCalls,
		ToolResults:   last.ToolResults,
		FinishReason:  last.FinishReason,
		Usage:         totalUsage,
		Messages:      messages,
	}, nil
}

// toolResultValue returns the value to send to the model for a tool result:
// the error string when the tool call failed, otherwise the tool's result.
func toolResultValue(r ToolResultRecord) any {
	if r.Err != nil {
		return r.Err.Error()
	}
	return r.Result
}

// runToolCalls executes calls sequentially in order against tools. It first
// validates that every call references a known tool, returning a
// *NoSuchToolError without executing anything if not (so earlier calls in
// the batch never run with real side effects just because a later one is
// unknown). Once validated, all calls are executed; an
// InvalidToolArgumentsError/ToolExecutionError from a tool's Execute is
// recorded on the corresponding ToolResultRecord.Err rather than aborting.
func runToolCalls(ctx context.Context, tools []Tool, calls []provider.ToolCallPart) ([]ToolResultRecord, error) {
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		byName[t.Name()] = t
	}

	for _, c := range calls {
		if _, ok := byName[c.Name]; !ok {
			return nil, &NoSuchToolError{ToolName: c.Name}
		}
	}

	results := make([]ToolResultRecord, 0, len(calls))
	for _, c := range calls {
		t := byName[c.Name]
		res, err := t.Execute(ctx, c.Args)
		results = append(results, ToolResultRecord{
			ToolCallID: c.ID,
			Name:       c.Name,
			Result:     res,
			Err:        err,
		})
	}
	return results, nil
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
		Text:          resp.Text(),
		ReasoningText: resp.ReasoningText(),
		Sources:       resp.SourceParts(),
		ToolCalls:     toolCalls,
		FinishReason:  resp.FinishReason,
		Usage:         resp.Usage,
		Response:      resp,
	}
}

// WrapModel applies wrap to m, returning the wrapped model. It is a
// one-line hook for middleware that decorates a provider.LanguageModel
// (e.g. logging, caching, retries) before it is passed to GenerateText.
func WrapModel(m provider.LanguageModel, wrap func(provider.LanguageModel) provider.LanguageModel) provider.LanguageModel {
	return wrap(m)
}
