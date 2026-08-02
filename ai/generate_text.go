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
	fail := func(err error) (*GenerateTextResult, error) {
		if opts.OnError != nil {
			opts.OnError(err)
		}
		return nil, err
	}

	call, err := buildCall(opts)
	if err != nil {
		// Argument-validation errors are reported solely via the returned
		// error, not OnError — this mirrors StreamText, which never reaches
		// the point of returning a *TextStream (and thus never has an
		// OnError-bearing call site to fire) when buildCall fails.
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
	active := activeToolSet(opts.ActiveTools)

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
				return fail(&RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr})
			}
			return fail(err)
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
			results, err := runToolCalls(ctx, opts.Tools, toolCalls, active, opts.RepairToolCall)
			if err != nil {
				return fail(err)
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

	result := &GenerateTextResult{
		Text:          last.Text,
		ReasoningText: last.ReasoningText,
		Sources:       last.Sources,
		Steps:         steps,
		ToolCalls:     last.ToolCalls,
		ToolResults:   last.ToolResults,
		FinishReason:  last.FinishReason,
		Usage:         totalUsage,
		Messages:      messages,
	}
	if opts.OnFinish != nil {
		opts.OnFinish(result)
	}
	return result, nil
}

// toolResultValue returns the value to send to the model for a tool result:
// the error string when the tool call failed, otherwise the tool's result.
func toolResultValue(r ToolResultRecord) any {
	if r.Err != nil {
		return r.Err.Error()
	}
	return r.Result
}

// repairFunc matches GenerateTextOpts.RepairToolCall's signature; named here
// so runToolCalls's signature stays readable.
type repairFunc func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool)

// runToolCalls executes calls sequentially in order against tools. It first
// validates that every call references a known tool — one present in tools
// and, if active is non-nil, also named in active — returning a
// *NoSuchToolError without executing anything if not (so earlier calls in
// the batch never run with real side effects just because a later one is
// unknown). If repair is non-nil, an unknown-tool call is offered to repair
// once before that abort: repair may return a corrected ToolCallRecord,
// which is re-validated in its place; a second failure (still unknown) is
// not retried again and aborts as usual.
//
// Once every call is validated (post-repair, if applicable), all calls are
// executed in order. An *InvalidToolArgumentsError from a tool's Execute is
// likewise offered to repair once (if non-nil): repair may return a
// corrected call, which is looked up and executed once more in its place;
// whatever that second attempt produces (success or another error) is
// recorded on the corresponding ToolResultRecord.Err rather than retried
// again. A *ToolExecutionError, or any InvalidToolArgumentsError when repair
// is nil or declines, is recorded on ToolResultRecord.Err rather than
// aborting.
func runToolCalls(ctx context.Context, tools []Tool, calls []provider.ToolCallPart, active map[string]bool, repair repairFunc) ([]ToolResultRecord, error) {
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if active != nil && !active[t.Name()] {
			continue
		}
		byName[t.Name()] = t
	}

	resolved := make([]provider.ToolCallPart, len(calls))
	for i, c := range calls {
		if _, ok := byName[c.Name]; ok {
			resolved[i] = c
			continue
		}
		var toolErr error = &NoSuchToolError{ToolName: c.Name}
		if repair != nil {
			rc, ok := repair(ctx, ToolCallRecord{ID: c.ID, Name: c.Name, Args: c.Args}, toolErr)
			if ok {
				fixed := provider.ToolCallPart{ID: rc.ID, Name: rc.Name, Args: rc.Args}
				if _, ok2 := byName[fixed.Name]; ok2 {
					resolved[i] = fixed
					continue
				}
				toolErr = &NoSuchToolError{ToolName: fixed.Name}
			}
		}
		return nil, toolErr
	}

	results := make([]ToolResultRecord, 0, len(resolved))
	for _, c := range resolved {
		t := byName[c.Name]
		res, err := t.Execute(ctx, c.Args)
		if err != nil && repair != nil {
			var iae *InvalidToolArgumentsError
			if errors.As(err, &iae) {
				rc, ok := repair(ctx, ToolCallRecord{ID: c.ID, Name: c.Name, Args: c.Args}, err)
				if ok {
					if rt, known := byName[rc.Name]; known {
						res, err = rt.Execute(ctx, rc.Args)
						c.ID, c.Name = rc.ID, rc.Name
					}
					// else: repair renamed the call to a tool that isn't in
					// the active set either; res/err are left as the
					// original InvalidToolArgumentsError rather than
					// re-validated into a NoSuchToolError — this retry path
					// is only entered for bad-args repair, so an unknown
					// name here is recorded as-is, not retried again.
				}
			}
		}
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
