package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	// Output holds the decoded value when GenerateTextOpts.Output was set:
	// a T for OutputObject[T], a []T for OutputArray[T], a string for
	// OutputChoice, or an arbitrary JSON value (map[string]any / []any /
	// ...) for OutputJSON. Nil when Output was not set. Extract it with
	// OutputAs[T].
	//
	// In the tool-mode fallback (Model.Capabilities().NativeJSON false),
	// the model is forced to call a single injected output-schema tool;
	// that call is never a real tool call as far as the rest of the
	// result is concerned. FinishReason is never tool-calls for it (the
	// underlying response's finish reason is kept when it's something
	// else, e.g. length; tool-calls itself is mapped to FinishStop), and
	// ToolCalls is empty on both the final Step and GenerateTextResult —
	// the forced call is scrubbed from both, not left dangling. Messages
	// ends with the assistant message carrying that tool call followed by
	// a synthetic RoleTool message answering it (a single ToolResultPart
	// whose Result is the call's own args JSON as a string), so the
	// returned transcript is always well-formed for a round-trip resend —
	// no provider's wire format is left with an unanswered tool call.
	//
	// A suspended run (PendingApprovals non-empty) has no Output — decoding
	// is skipped entirely for it, even if Output was set, since the
	// suspended step's Text has nothing to do with the output schema (the
	// batch never executed). Output is decoded on the RESUMED run that
	// actually completes.
	Output any
	// PendingApprovals is non-empty when the tool loop suspended because
	// some call(s) in a batch needed approval (see ApprovalRequirer) and no
	// decision was available for them (checked against Approvals, then
	// ApproveToolCall). Batch atomicity: when this is non-empty, NO tool in
	// that batch executed, even ones that needed no approval or were
	// already decided. One entry per approval-needing call without a
	// decision, in call order. Messages ends with the assistant tool-call
	// message (round-trippable — resend it, with Approvals set to answer
	// these calls, to resume). Not an error: OnFinish still fires, and
	// FinishReason is the step's real finish reason (tool-calls).
	//
	// When a RESUMED batch (Messages ending in an unanswered assistant
	// tool-call message) is itself still pending, the run suspends before
	// any model call: Steps is empty, Messages is unchanged, and
	// FinishReason is tool-calls.
	//
	// A suspended run has no Output (see that field's doc), even when
	// GenerateTextOpts.Output was set — decoding is deferred to the resumed
	// run that completes.
	PendingApprovals []ApprovalRequest
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

	call, outputToolName, err := buildOutputCall(opts)
	if err != nil {
		// Argument-validation errors are reported solely via the returned
		// error, not OnError — this mirrors StreamText, which never reaches
		// the point of returning a *TextStream (and thus never has an
		// OnError-bearing call site to fire) when buildCall fails.
		return nil, err
	}

	// RuntimeContext is installed once, before the loop (and before the
	// resume batch, if any) — see GenerateTextOpts.RuntimeContext.
	ctx = withRuntimeContext(ctx, opts.RuntimeContext)

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
	var pendingApprovals []ApprovalRequest
	model := opts.Model

	// Resume: an unanswered assistant tool-call batch at the end of
	// Messages is run first — approval rules applied — before the first
	// model call. See GenerateTextOpts.Messages's resume-semantics doc.
	if resumeCalls := trailingUnansweredToolCalls(messages); len(resumeCalls) > 0 {
		batch, berr := runApprovalAwareToolCalls(ctx, opts, opts.Tools, resumeCalls, active, 0, true)
		if berr != nil {
			return fail(berr)
		}
		if len(batch.pending) > 0 {
			result := &GenerateTextResult{
				Messages:         messages,
				FinishReason:     provider.FinishToolCalls,
				PendingApprovals: batch.pending,
			}
			if opts.OnFinish != nil {
				opts.OnFinish(result)
			}
			return result, nil
		}
		messages = append(messages, toolResultMessage(batch.results))
	}

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

		if opts.OnModelCallStart != nil {
			opts.OnModelCallStart(stepIndex, stepCall)
		}

		resp, err := retry.Do(ctx, maxRetries, func() (*provider.Response, error) {
			return model.Generate(ctx, stepCall)
		})
		callErr := translateRetryErr(err)

		if opts.OnModelCallEnd != nil {
			end := ModelCallEnd{StepIndex: stepIndex, Err: callErr}
			if callErr == nil {
				end.Response = resp
				end.Usage = resp.Usage
				end.FinishReason = resp.FinishReason
			}
			opts.OnModelCallEnd(end)
		}

		if callErr != nil {
			return fail(callErr)
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

		if outputToolName != "" && hasToolCalls {
			// Output's tool-mode fallback: the model was forced (via
			// ToolChoice) to call the single injected output-schema tool.
			// Find that call by name (mirrors GenerateObject's guard) — a
			// model that hallucinates a call to some other tool name is not
			// a valid structured-output response, so it must not be decoded
			// silently.
			matched, ok := findToolCallByName(toolCalls, outputToolName)
			if !ok {
				return fail(&NoObjectGeneratedError{
					RawText: resp.Text(),
					Cause:   fmt.Errorf("ai: output: model did not call the output tool %q", outputToolName),
				})
			}

			// That call is not a real tool — it must not be executed via
			// runToolCalls. Its Args are the structured output's raw JSON,
			// which becomes this (single, final) step's Text. Scrub it from
			// ToolCalls (this step's, and thus the eventual Result's — see
			// GenerateTextResult.Output's doc) so callers keying off
			// FinishReason == tool-calls or ToolCalls length don't
			// misclassify this step as an unanswered tool call.
			step.Text = string(matched.Args)
			step.ToolCalls = nil

			// The transcript must not end with a dangling (unanswered)
			// assistant tool call — several providers 400 on a round-trip
			// resend of a conversation whose last assistant message has an
			// unanswered tool_use/tool_calls block. Append a synthetic
			// RoleTool result message answering the forced call; its text
			// is the call's own args JSON (round-trips as a plain string
			// through every provider's tool-result wire encoding — see
			// GenerateTextResult.Output's doc for the exact transcript
			// shape this produces).
			messages = append(messages, provider.Message{
				Role: provider.RoleTool,
				Content: []provider.ContentPart{provider.ToolResultPart{
					ToolCallID: matched.ID,
					Name:       matched.Name,
					Result:     string(matched.Args),
				}},
			})

			// The step's true finish reason is "the structured output
			// completed", not "tool-calls": with ToolCalls now scrubbed to
			// empty, reporting FinishToolCalls would leave FinishReason and
			// ToolCalls contradicting each other. Use the underlying
			// response's finish reason when it's informative (e.g. length,
			// content-filter); map tool-calls itself to FinishStop.
			if resp.FinishReason == provider.FinishToolCalls {
				step.FinishReason = provider.FinishStop
			}

			steps = append(steps, step)
			if opts.OnStepFinish != nil {
				opts.OnStepFinish(step)
			}
			break
		}

		if hasToolCalls {
			batch, err := runApprovalAwareToolCalls(ctx, opts, opts.Tools, toolCalls, active, stepIndex, false)
			if err != nil {
				return fail(err)
			}
			if len(batch.pending) > 0 {
				// Batch atomicity: nothing in this batch executed. The step
				// is still recorded (tool-result-less) and OnStepFinish
				// still fires, but the loop stops here — see
				// GenerateTextResult.PendingApprovals.
				pendingApprovals = batch.pending
				steps = append(steps, step)
				if opts.OnStepFinish != nil {
					opts.OnStepFinish(step)
				}
				break
			}
			step.ToolResults = batch.results
			messages = append(messages, toolResultMessage(batch.results))
		}

		steps = append(steps, step)

		if opts.OnStepFinish != nil {
			opts.OnStepFinish(step)
		}

		// StopWhen is consulted after every step (even one with no tool
		// calls) — see GenerateTextOpts.StopWhen. Evaluating it here,
		// unconditionally, doesn't change when the loop actually stops for
		// a no-tool-call step (that's already handled below), but it does
		// mean a StopWhen closure with side effects (or one composed with
		// LoopFinished) observes every step, not just tool-call ones.
		stopByCondition := opts.StopWhen != nil && opts.StopWhen(steps)

		if !hasToolCalls {
			break
		}
		if len(steps) >= maxSteps {
			break
		}
		if stopByCondition {
			break
		}
	}

	last := steps[len(steps)-1]

	// Output decoding is skipped entirely when the run suspended (see
	// GenerateTextResult.PendingApprovals): the suspended step's Text has
	// nothing to do with the output schema (the batch never executed, let
	// alone produced a final structured-output response), so decoding it
	// would either fail — turning a non-error suspension into a
	// *NoObjectGeneratedError — or spuriously "succeed" on unrelated text.
	// result.Output stays nil; it is decoded on the resumed run that
	// actually completes.
	var decoded any
	if opts.Output != nil && len(pendingApprovals) == 0 {
		raw := stripFences(last.Text)
		val, derr := opts.Output.decode(raw)
		if derr != nil {
			return fail(derr)
		}
		decoded = val
	}

	result := &GenerateTextResult{
		Text:             last.Text,
		ReasoningText:    last.ReasoningText,
		Sources:          last.Sources,
		Steps:            steps,
		ToolCalls:        last.ToolCalls,
		ToolResults:      last.ToolResults,
		FinishReason:     last.FinishReason,
		Usage:            totalUsage,
		Messages:         messages,
		Output:           decoded,
		PendingApprovals: pendingApprovals,
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
func runToolCalls(ctx context.Context, tools []Tool, calls []provider.ToolCallPart, active map[string]bool, repair repairFunc, stepIndex int, onStart func(int, ToolCallRecord), onEnd func(int, ToolResultRecord, error)) ([]ToolResultRecord, error) {
	byName := buildActiveToolMap(tools, active)
	resolved, err := resolveToolCallNames(ctx, byName, calls, repair)
	if err != nil {
		return nil, err
	}
	results := make([]ToolResultRecord, 0, len(resolved))
	for _, c := range resolved {
		results = append(results, executeToolCall(ctx, byName, c, nil, repair, stepIndex, onStart, onEnd))
	}
	return results, nil
}

// buildActiveToolMap indexes tools by name, dropping any not in active (nil
// active means every tool is active — see activeToolSet).
func buildActiveToolMap(tools []Tool, active map[string]bool) map[string]Tool {
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if active != nil && !active[t.Name()] {
			continue
		}
		byName[t.Name()] = t
	}
	return byName
}

// resolveToolCallNames validates that every call in calls names a tool in
// byName, offering an unknown name to repair once (see RepairToolCall's
// doc); it returns a *NoSuchToolError without resolving anything further if
// any call (post-repair) still names an unknown tool — so earlier calls in
// the batch never execute just because a later one is unknown.
func resolveToolCallNames(ctx context.Context, byName map[string]Tool, calls []provider.ToolCallPart, repair repairFunc) ([]provider.ToolCallPart, error) {
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
	return resolved, nil
}

// executeToolCall executes a single already-validated call. When decision is
// non-nil and not Approved, Execute is never called: the recorded result
// carries a *ToolApprovalDeniedError instead (see ApprovalDecision.Reason).
// Otherwise (decision nil, meaning no approval was required, or Approved)
// it executes normally, offering an *InvalidToolArgumentsError to repair
// once, exactly as before approvals existed. OnToolExecutionStart/End fire
// exactly once around this call either way — including for a denial, which
// is why they're placed here rather than in the caller's pending/no-pending
// branch.
// fireOnInputAvailable invokes t's ToolInputCallbacks.OnInputAvailable, if
// set, with the call's fully assembled arguments — shared by GenerateText's
// and StreamText's tool loops (both funnel through executeToolCall), since
// in both cases OnInputAvailable fires once per call, immediately before
// Execute. It is nil-checked and never fires for a call that is never
// actually executed (e.g. an approval-denied call, or the Output tool-mode
// synthetic call, which never reaches executeToolCall at all).
func fireOnInputAvailable(ctx context.Context, t Tool, toolCallID string, args json.RawMessage) {
	if cb := t.InputCallbacks(); cb.OnInputAvailable != nil {
		cb.OnInputAvailable(ctx, toolCallID, args)
	}
}

func executeToolCall(ctx context.Context, byName map[string]Tool, c provider.ToolCallPart, decision *ApprovalDecision, repair repairFunc, stepIndex int, onStart func(int, ToolCallRecord), onEnd func(int, ToolResultRecord, error)) ToolResultRecord {
	if onStart != nil {
		onStart(stepIndex, ToolCallRecord{ID: c.ID, Name: c.Name, Args: c.Args})
	}

	if decision != nil && !decision.Approved {
		deniedErr := &ToolApprovalDeniedError{ToolName: c.Name, Reason: decision.Reason}
		result := ToolResultRecord{ToolCallID: c.ID, Name: c.Name, Err: deniedErr}
		if onEnd != nil {
			onEnd(stepIndex, result, deniedErr)
		}
		return result
	}

	t := byName[c.Name]
	fireOnInputAvailable(ctx, t, c.ID, c.Args)
	res, err := t.Execute(ctx, c.Args)
	if err != nil && repair != nil {
		var iae *InvalidToolArgumentsError
		if errors.As(err, &iae) {
			rc, ok := repair(ctx, ToolCallRecord{ID: c.ID, Name: c.Name, Args: c.Args}, err)
			if ok {
				if rt, known := byName[rc.Name]; known {
					c.ID, c.Name = rc.ID, rc.Name
					// The repaired call is re-checked against
					// ApprovalRequirer before it executes — repair can
					// rename the call to a different (approval-requiring)
					// tool, or change the args of an already-approved
					// approval-requiring call, and either way the
					// decision that let this call reach executeToolCall
					// (if any) was made against the ORIGINAL call, not
					// this one. There is no suspension possible
					// mid-execution, so a repaired call that now requires
					// approval is recorded as a denial rather than
					// executed or silently allowed through. See
					// GenerateTextOpts.RepairToolCall's doc.
					if ar, needsApproval := rt.(ApprovalRequirer); needsApproval && ar.ApprovalRequired(ctx, rc.Args) {
						res, err = nil, &ToolApprovalDeniedError{ToolName: rc.Name, Reason: "approval required for repaired call"}
					} else {
						fireOnInputAvailable(ctx, rt, rc.ID, rc.Args)
						res, err = rt.Execute(ctx, rc.Args)
					}
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
	result := ToolResultRecord{
		ToolCallID: c.ID,
		Name:       c.Name,
		Result:     res,
		Err:        err,
	}
	if onEnd != nil {
		onEnd(stepIndex, result, err)
	}
	return result
}

// toolBatchResult is the outcome of runApprovalAwareToolCalls: either
// Results (every call in the batch executed, possibly some denied) or
// Pending (batch atomicity — nothing executed; see GenerateTextResult.
// PendingApprovals).
type toolBatchResult struct {
	results []ToolResultRecord
	pending []ApprovalRequest
}

// runApprovalAwareToolCalls is runToolCalls plus approval handling (see
// GenerateTextOpts.ApproveToolCall/Approvals and ApprovalRequirer). It
// validates unknown tool names first (same as runToolCalls — rule: approval
// checks happen after unknown-tool validation, before execution/repair),
// then resolves an ApprovalDecision for every call whose Tool implements
// ApprovalRequirer and reports true. consultApprovals gates whether
// opts.Approvals is checked at all: true only for the RESUME batch (the
// batch pending at entry from Messages — see Messages's resume-semantics
// doc), false for every batch arising later within the same run. This
// matters because opts.Approvals matches by ToolCallID alone, and some
// providers (e.g. geminicompat) synthesize deterministic IDs — a later
// batch's call can legitimately reuse an ID an earlier Approvals entry
// already answered, which would otherwise auto-approve a call no one ever
// actually decided. opts.ApproveToolCall has no such collision risk (it's
// consulted live, per call) and is always tried regardless of
// consultApprovals. Any call left undecided makes the WHOLE batch pending —
// nothing executes, and Pending lists every undecided call, in call order.
func runApprovalAwareToolCalls(ctx context.Context, opts GenerateTextOpts, tools []Tool, calls []provider.ToolCallPart, active map[string]bool, stepIndex int, consultApprovals bool) (*toolBatchResult, error) {
	byName := buildActiveToolMap(tools, active)
	resolved, err := resolveToolCallNames(ctx, byName, calls, opts.RepairToolCall)
	if err != nil {
		return nil, err
	}

	decisions := make(map[string]*ApprovalDecision, len(resolved))
	var pending []ApprovalRequest
	for _, c := range resolved {
		ar, ok := byName[c.Name].(ApprovalRequirer)
		if !ok || !ar.ApprovalRequired(ctx, c.Args) {
			continue
		}
		rec := ToolCallRecord{ID: c.ID, Name: c.Name, Args: c.Args}
		if consultApprovals {
			if d, found := findApprovalDecision(opts.Approvals, c.ID); found {
				decisions[c.ID] = &d
				continue
			}
		}
		if opts.ApproveToolCall != nil {
			if d, ok := opts.ApproveToolCall(ctx, ApprovalRequest{StepIndex: stepIndex, Call: rec}); ok {
				decisions[c.ID] = &d
				continue
			}
		}
		pending = append(pending, ApprovalRequest{StepIndex: stepIndex, Call: rec})
	}

	if len(pending) > 0 {
		return &toolBatchResult{pending: pending}, nil
	}

	results := make([]ToolResultRecord, 0, len(resolved))
	for _, c := range resolved {
		results = append(results, executeToolCall(ctx, byName, c, decisions[c.ID], opts.RepairToolCall, stepIndex, opts.OnToolExecutionStart, opts.OnToolExecutionEnd))
	}
	return &toolBatchResult{results: results}, nil
}

// toolResultMessage builds the RoleTool message carrying results, in order —
// used by both the normal in-loop batch and the resume path.
func toolResultMessage(results []ToolResultRecord) provider.Message {
	parts := make([]provider.ContentPart, 0, len(results))
	for _, r := range results {
		parts = append(parts, provider.ToolResultPart{
			ToolCallID: r.ToolCallID,
			Name:       r.Name,
			Result:     toolResultValue(r),
			IsError:    r.Err != nil,
		})
	}
	return provider.Message{Role: provider.RoleTool, Content: parts}
}

// trailingUnansweredToolCalls returns the ToolCallParts of the last message
// in messages when it is an assistant message containing tool calls — since
// it's the last message, no RoleTool message answers it yet. Returns nil
// otherwise (including when messages is empty, or the last message is an
// assistant message with no tool calls at all). See GenerateTextOpts.
// Messages's resume-semantics doc.
func trailingUnansweredToolCalls(messages []provider.Message) []provider.ToolCallPart {
	if len(messages) == 0 {
		return nil
	}
	last := messages[len(messages)-1]
	if last.Role != provider.RoleAssistant {
		return nil
	}
	var calls []provider.ToolCallPart
	for _, part := range last.Content {
		if tc, ok := part.(provider.ToolCallPart); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// findToolCallByName returns the first call in calls whose Name is name.
// Used by GenerateText's Output tool-mode fallback to identify the forced
// output-schema tool's call among (in principle, though the model is
// supposed to only make one) the model's tool calls.
func findToolCallByName(calls []provider.ToolCallPart, name string) (provider.ToolCallPart, bool) {
	for _, c := range calls {
		if c.Name == name {
			return c, true
		}
	}
	return provider.ToolCallPart{}, false
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

// WrapImageModel applies wrap to m, returning the wrapped model. It is the
// provider.ImageModel counterpart to WrapModel — a one-line naming hook for
// middleware that decorates a provider.ImageModel before it is passed to
// GenerateImage.
func WrapImageModel(m provider.ImageModel, wrap func(provider.ImageModel) provider.ImageModel) provider.ImageModel {
	return wrap(m)
}
