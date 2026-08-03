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
	// TopK, PresencePenalty, FrequencyPenalty, and Seed are threaded through
	// to the identically-named provider.Call fields unchanged — see those
	// fields' docs for per-provider support and wire-name mapping.
	TopK             *int
	PresencePenalty  *float64
	FrequencyPenalty *float64
	Seed             *int64
	// Headers carries extra HTTP headers to send with the request; threaded
	// through to provider.Call.Headers unchanged — see that field's doc for
	// precedence (it never overrides the provider's auth header) and which
	// request paths implement it.
	Headers map[string]string

	// StopWhen, when set, decides after each completed step whether to stop
	// the tool loop (return true = stop). It is evaluated after EVERY step,
	// whether or not that step requested tool calls — this is what makes
	// LoopFinished (which reports true on a no-tool-call step) meaningful to
	// compose with other conditions inside a custom StopWhen. That said, a
	// step with no tool calls always ends the loop naturally regardless of
	// what StopWhen returns for it — StopWhen cannot make the loop continue
	// past a step the model didn't request further tool calls in. MaxSteps
	// still applies as a hard cap regardless of StopWhen: if MaxSteps is 0
	// (unset) and StopWhen is non-nil, the hard cap defaults to 16 instead
	// of the usual default of 1.
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
	// It observes the raw parts the provider produced: if the result is
	// wrapped with SmoothStream, that re-chunking happens downstream of
	// OnChunk, so OnChunk still sees the provider's original, unsmoothed
	// parts rather than the re-chunked ones the consumer ultimately reads.
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

	// OnAbort, when set, is consulted only by StreamText (GenerateText has
	// no notion of an abandoned or mid-flight iteration, so it never calls
	// OnAbort). It fires exactly once per TextStream, before the stream's
	// internal Close, in either of two cases:
	//
	//   - the consumer abandons TextStream.Parts() early — stops ranging
	//     over it (e.g. via break) before the tool loop would otherwise have
	//     ended naturally or with an error;
	//   - the context passed to StreamText is canceled (or its deadline
	//     exceeded) while a step's stream is in flight, surfacing as that
	//     step's stream.Err().
	//
	// OnAbort never fires on natural completion (StopWhen/MaxSteps/no more
	// tool calls) — that case is OnFinish's — nor does it fire together with
	// OnError for the same event: a ctx-cancellation mid-stream fires
	// OnAbort only, while any other mid-stream error (a real provider
	// failure, not caused by ctx) fires OnError only. Abandoning iteration
	// early is likewise never accompanied by an error — Err() reports nil
	// in that case, same as before this field existed — so only OnAbort
	// fires for it.
	OnAbort func()

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

// HasToolCall returns a StopWhen function that stops the tool loop when the
// LAST completed step called any of the named tools. With no names given,
// it stops when the last step called any tool at all (regardless of name).
// An empty steps slice (impossible in normal use, since StopWhen is only
// ever consulted after at least one step has completed) reports false.
func HasToolCall(names ...string) func([]Step) bool {
	return func(steps []Step) bool {
		if len(steps) == 0 {
			return false
		}
		last := steps[len(steps)-1].ToolCalls
		if len(names) == 0 {
			return len(last) > 0
		}
		want := make(map[string]bool, len(names))
		for _, n := range names {
			want[n] = true
		}
		for _, tc := range last {
			if want[tc.Name] {
				return true
			}
		}
		return false
	}
}

// LoopFinished returns a StopWhen function that stops the tool loop when the
// LAST completed step made no tool calls — the same condition that already
// ends the loop naturally (see StopWhen's doc comment: a no-tool-call step
// always ends the loop, independent of what any StopWhen function returns).
// It is provided mainly for composing with other conditions inside a custom
// StopWhen closure (e.g. "stop at 10 steps OR when the loop would finish
// naturally, whichever comes first" — though for that particular example
// the natural-end behavior already makes the LoopFinished half redundant;
// it earns its keep in less trivial compositions).
func LoopFinished() func([]Step) bool {
	return func(steps []Step) bool {
		if len(steps) == 0 {
			return false
		}
		return len(steps[len(steps)-1].ToolCalls) == 0
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
		Messages:         messages,
		Tools:            toolDefs,
		ToolChoice:       opts.ToolChoice,
		MaxTokens:        opts.MaxTokens,
		Temperature:      opts.Temperature,
		TopP:             opts.TopP,
		StopSequences:    opts.StopSequences,
		TopK:             opts.TopK,
		PresencePenalty:  opts.PresencePenalty,
		FrequencyPenalty: opts.FrequencyPenalty,
		Seed:             opts.Seed,
		Headers:          opts.Headers,
		ProviderOptions:  opts.ProviderOptions,
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
