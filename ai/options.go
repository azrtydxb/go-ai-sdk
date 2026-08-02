package ai

import (
	"context"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// GenerateTextOpts configures a GenerateText call.
type GenerateTextOpts struct {
	Model         provider.LanguageModel // required
	System        string                 // optional; prepended as system message
	Prompt        string                 // exactly one of Prompt/Messages
	Messages      []provider.Message
	Tools         []Tool
	ToolChoice    *provider.ToolChoice
	MaxSteps      int  // default 1; if 0 and StopWhen is set, defaults to 16
	MaxRetries    *int // default 2
	MaxTokens     *int
	Temperature   *float64
	TopP          *float64
	StopSequences []string

	// StopWhen, when set, decides after each completed step whether to stop
	// the tool loop (return true = stop). It is evaluated only when that
	// step requested tool calls — a step with no tool calls always ends the
	// loop naturally. MaxSteps still applies as a hard cap regardless of
	// StopWhen: if MaxSteps is 0 (unset) and StopWhen is non-nil, the hard
	// cap defaults to 16 instead of the usual default of 1.
	StopWhen func(steps []Step) bool
	// PrepareStep, when set, is called before each model call with the
	// zero-based step index and the StepPlan about to be used: Call is the
	// Call about to be sent, and Model is the model that will make the
	// call (opts.Model on step 0, or whatever an earlier PrepareStep call
	// last swapped to). Returning (plan, true) uses the returned StepPlan
	// for that step instead; returning (_, false) leaves the planned step
	// unchanged.
	//
	// Setting StepPlan.Model swaps the model used for that step's call —
	// and every step after it, until PrepareStep swaps again (StepPlan.Model
	// persists rather than applying to a single step). This is a deliberate
	// divergence from a strictly per-step swap: it composes more simply (a
	// swap made at step N doesn't need to be re-asserted at every later step
	// to "stick") and matches the common use case of routing to a different
	// model partway through a run (e.g. a cheaper model once a plan has been
	// established) rather than alternating models step by step.
	PrepareStep func(stepIndex int, plan StepPlan) (StepPlan, bool)
	// OnStepFinish, when set, is called after each step completes
	// (including the final step) in both GenerateText and StreamText, with
	// the finished Step. Errors are not returned from the callback.
	//
	// Caveat for StreamText: the callback fires only once a step's Parts()
	// iteration has run to completion. If the consumer stops ranging over
	// Parts() before that (e.g. breaking out of the loop right after
	// observing that step's FinishPart), OnStepFinish does not fire for
	// that step, even though FinishPart itself was already delivered.
	OnStepFinish func(step Step)

	// OnChunk, when set, is called with each provider.StreamPart before it
	// is yielded to the consumer of StreamText's TextStream.Parts(). It has
	// no effect on GenerateText, which has no stream of parts to observe.
	OnChunk func(part provider.StreamPart)

	// OnFinish, when set, is called once with the call's result after it
	// completes successfully: in GenerateText, right before it returns,
	// with the same *GenerateTextResult that is returned; in StreamText, at
	// the natural end of TextStream.Parts() iteration (the tool loop
	// stopped because a step had no tool calls, MaxSteps was reached, or
	// StopWhen returned true) — never on a step that ended in an error, nor
	// if the consumer abandons iteration (stops ranging) before the stream
	// ends naturally. The StreamText case builds a fresh *GenerateTextResult
	// from the same accumulated step/usage/message state exposed by
	// TextStream's Steps/Usage/Messages accessors, so it is equivalent in
	// shape to what GenerateText would return for the same underlying model
	// script.
	OnFinish func(result *GenerateTextResult)

	// OnError, when set, is called with a call's terminal error. In
	// StreamText this covers errors that end TextStream.Parts() iteration
	// abnormally — a mid-stream provider error (TextStream.Err()) or a
	// tool-loop error (e.g. an unknown tool, or a subsequent step's stream
	// failing to start) — but not a failure to start the very first stream,
	// which is reported solely via the error StreamText itself returns. In
	// GenerateText, the function's returned error already fully signals
	// failure to the caller; OnError additionally fires with that same
	// error for symmetry with StreamText, so code that wires up both APIs
	// through one callback doesn't have to special-case GenerateText.
	//
	// OnError is not invoked for argument-validation errors (nil model,
	// prompt/messages misuse) — those are reported solely via the returned
	// error, in both APIs. This mirrors StreamText, which never reaches a
	// call site capable of invoking OnError when that validation fails
	// (buildCall runs before the first model call, so there's no started
	// call for OnError to describe); GenerateText applies the same
	// exclusion for consistency, even though it could technically fire
	// OnError there.
	OnError func(err error)

	// ProviderOptions carries provider-specific escape-hatch parameters. It
	// is threaded through to provider.Call.ProviderOptions unchanged — see
	// that field's doc for the keying and merge semantics.
	ProviderOptions map[string]any

	// ActiveTools, when non-nil, limits which of Tools are OFFERED to the
	// model (ToolDefs in the Call built by buildCall — PrepareStep, which
	// runs afterward, sees the already-filtered call and may replace
	// Call.Tools itself). Execution is restricted the same way: a call
	// (whether from the model directly or a corrected call returned by
	// RepairToolCall) that names a tool outside the active set is treated
	// as unknown — a *NoSuchToolError — even though that tool is present in
	// Tools. A nil ActiveTools means all of Tools are active; a non-nil,
	// possibly empty, slice replaces the active set entirely.
	ActiveTools []string
	// RepairToolCall is invoked when a tool call fails to validate — an
	// unknown tool name (not in the active set), or an
	// *InvalidToolArgumentsError from the tool's Execute. It may return a
	// corrected call (retried ONCE per original call) or false to give up,
	// in which case the original error's normal semantics apply (a
	// *NoSuchToolError aborts the batch; an *InvalidToolArgumentsError is
	// recorded on the corresponding ToolResultRecord.Err). Repair runs
	// before either of those normal-path outcomes. If the repaired call
	// fails again — still unknown, or Execute fails again — RepairToolCall
	// is not invoked a second time for that original call.
	RepairToolCall func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool)
}

// StepCountIs returns a StopWhen function that stops the tool loop once at
// least n steps have completed.
func StepCountIs(n int) func([]Step) bool {
	return func(steps []Step) bool {
		return len(steps) >= n
	}
}

// buildCall converts a GenerateTextOpts into a provider.Call, prepending a
// system message (if set) and converting either Prompt or Messages into
// provider messages, and Tools into ToolDefs.
//
// It returns an error if Model is nil, or if both/neither of Prompt and
// Messages are set.
func buildCall(opts GenerateTextOpts) (provider.Call, error) {
	if opts.Model == nil {
		return provider.Call{}, errors.New("ai: GenerateTextOpts.Model is required")
	}
	hasPrompt := opts.Prompt != ""
	hasMessages := len(opts.Messages) > 0
	if hasPrompt == hasMessages {
		return provider.Call{}, errors.New("ai: exactly one of Prompt or Messages must be set")
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

	active := activeToolSet(opts.ActiveTools)
	var toolDefs []provider.ToolDef
	for _, t := range opts.Tools {
		if active != nil && !active[t.Name()] {
			continue
		}
		toolDefs = append(toolDefs, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}

	return provider.Call{
		Messages:        messages,
		Tools:           toolDefs,
		ToolChoice:      opts.ToolChoice,
		MaxTokens:       opts.MaxTokens,
		Temperature:     opts.Temperature,
		TopP:            opts.TopP,
		StopSequences:   opts.StopSequences,
		ProviderOptions: opts.ProviderOptions,
	}, nil
}

// activeToolSet returns the set of active tool names from activeTools, or
// nil if activeTools is nil (meaning every tool is active — no filtering).
func activeToolSet(activeTools []string) map[string]bool {
	if activeTools == nil {
		return nil
	}
	set := make(map[string]bool, len(activeTools))
	for _, name := range activeTools {
		set[name] = true
	}
	return set
}
