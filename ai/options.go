package ai

import (
	"context"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// GenerateTextOpts configures a GenerateText call.
type GenerateTextOpts struct {
	Model  provider.LanguageModel // required
	System string                 // optional; prepended as system message
	Prompt string                 // exactly one of Prompt/Messages
	// Messages, when its LAST message is an assistant message containing
	// ToolCallParts (necessarily unanswered, since it's the last message —
	// no RoleTool message follows it), resumes a previously-suspended tool
	// loop: both GenerateText and StreamText run that batch first (approval
	// rules applied against Approvals/ApproveToolCall, same as any other
	// batch) before making any model call, append the RoleTool results
	// message, and proceed with the loop as usual. If that batch is itself
	// still pending (no decision available for some call), the run
	// suspends again immediately — see PendingApprovals — without ever
	// calling the model.
	Messages   []provider.Message
	Tools      []Tool
	ToolChoice *provider.ToolChoice
	// Output selects a structured-output mode (object/array/choice/json) for
	// this call. GenerateText-only: StreamText returns
	// ErrOutputWithStreamText immediately when Output is set. When Output
	// has a schema and Model.Capabilities().NativeJSON is true, it is
	// enforced via Call.ResponseFormat; when NativeJSON is false, it falls
	// back to a single forced tool call the same way GenerateObject does —
	// which requires Tools to be empty (ErrOutputRequiresJSONOrNoTools
	// otherwise). See Output's doc and OutputObject/OutputArray/
	// OutputChoice/OutputJSON for the available modes, and OutputAs to
	// extract the decoded GenerateTextResult.Output as a concrete type. In the
	// tool-mode fallback, if the model emits the output tool more than once in
	// one response, only the first matching call is decoded and answered.
	Output Output
	// SchemaDescription describes the expected output schema; used as the
	// injected output tool's Description in the tool-mode fallback. It has
	// no effect in native-JSON mode (ResponseFormat carries no description).
	SchemaDescription string
	MaxSteps          int  // default 1; if 0 and StopWhen is set, defaults to 16
	MaxRetries        *int // default 2
	MaxTokens         *int
	Temperature       *float64
	TopP              *float64
	StopSequences     []string
	// TopK, PresencePenalty, FrequencyPenalty, and Seed are threaded through
	// to the identically-named provider.Call fields unchanged — see those
	// fields' docs for per-provider support and wire-name mapping.
	TopK             *int
	PresencePenalty  *float64
	FrequencyPenalty *float64
	Seed             *int64
	// Reasoning is the unified reasoning/thinking request option. It is
	// threaded through to the identically-named provider.Call field
	// unchanged — see that field's doc for per-provider wire mapping.
	// ProviderOptions still merge last and win on wire-key collision.
	Reasoning *provider.ReasoningConfig
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
	//
	// One exception: the step that completes Output's tool-mode fallback
	// (see Output's doc) ends the loop unconditionally and does NOT
	// evaluate StopWhen at all — that step's forced tool call is the
	// structured output itself, so the loop must end there regardless of
	// what a StopWhen closure would otherwise decide.
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
	// established) rather than alternating models step by step. A model swapped
	// in via PrepareStep is not re-checked against Output's NativeJSON capability
	// requirement — the output strategy is fixed from opts.Model at entry; swapping
	// to a model without native JSON mid-loop leaves the schema unenforced on that
	// provider.
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
	//     exceeded), causing ANY step of the tool loop to terminate with an
	//     error — not just a step's stream itself (stream.Err()), but
	//     equally a between-steps failure while ctx is canceled: tool
	//     execution (runToolCalls), or building/starting the next step's
	//     call (buildCall/startStream). Wherever in the loop the
	//     cancellation is observed, the outcome is the same: OnAbort fires
	//     instead of OnError for that termination.
	//
	// OnAbort never fires on natural completion (StopWhen/MaxSteps/no more
	// tool calls) — that case is OnFinish's — nor does it fire together with
	// OnError for the same event: a ctx-cancellation-caused termination
	// anywhere in the loop fires OnAbort only, while any other error (a real
	// provider or tool failure, not caused by ctx) fires OnError only.
	// Abandoning iteration early is likewise never accompanied by an error —
	// Err() reports nil in that case, same as before this field existed —
	// so only OnAbort fires for it.
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
	//
	// Repair × approval ordering: a bad-args repair is re-checked against
	// ApprovalRequirer using the REPAIRED call's tool and args before it
	// executes — not the decision (if any) that let the ORIGINAL call reach
	// execution. Repair may rename the call to a different tool that
	// requires approval, or change the args of an already-approved
	// approval-requiring call (the approval covered the original args, not
	// the repaired ones); either way, there is no suspension possible
	// mid-execution, so a repaired call that now requires approval is
	// recorded as a denial (&ToolApprovalDeniedError{ToolName, Reason:
	// "approval required for repaired call"} on ToolResultRecord.Err)
	// rather than executed or silently allowed through.
	RepairToolCall func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool)

	// OnModelCallStart fires immediately before each underlying model
	// request (once per step, in both GenerateText and StreamText), with
	// the step index (0-based) and the exact provider.Call about to be
	// sent. It fires exactly once per step, outside the retry loop — before
	// the FIRST attempt, regardless of how many retries that step's call
	// ends up needing.
	OnModelCallStart func(stepIndex int, call provider.Call)
	// OnModelCallEnd fires when the model request for a step completes —
	// exactly once, after the FINAL attempt (success or retry exhaustion),
	// pairing with OnModelCallStart.
	//
	// In GenerateText, Response is the provider response (nil on error),
	// and Err — when non-nil — is the SAME error GenerateText itself
	// returns for that failure (retry exhaustion is translated to
	// *RetryError before this callback sees it, never the raw
	// retry-internal error).
	//
	// In StreamText, Response is always nil (there is no single Response
	// value for a stream); Usage/FinishReason carry what the step's
	// FinishPart reported (zero values if the step's stream errored before
	// a FinishPart arrived). On StreamText's abort path — see OnAbort: the
	// consumer abandoning iteration early, or ctx cancellation causing the
	// termination — OnModelCallEnd does NOT fire for that step; OnAbort
	// covers it instead, so every step's Start/End pair either both fire or
	// (on the abort path) neither Err-bearing End fires alone without an
	// abort signal.
	OnModelCallEnd func(end ModelCallEnd)
	// OnToolExecutionStart fires before each tool Execute (after the call
	// has been validated/repaired into a known tool, before the tool
	// runs). OnToolExecutionEnd fires after, with the result record and the
	// raw execution error (nil on success; note the loop also reports tool
	// errors through the result record's Err field, which mirrors this
	// argument). Both fire exactly once per tool call record: when
	// RepairToolCall retries a failed Execute, that whole
	// execute-and-maybe-repair sequence counts as one execution, not two —
	// there is one Start/End pair per original tool call, not one per
	// attempt. Neither callback fires for a call that never reaches
	// Execute at all (e.g. one that aborts the whole batch as an unknown
	// tool with no successful repair).
	//
	// If RepairToolCall's bad-args repair path (see its doc) changes the
	// call's ID or Name, the pair does NOT agree on those fields:
	// OnToolExecutionStart still fires with the ORIGINAL (pre-repair) ID
	// and Name (it fires before Execute is first attempted, when only the
	// original call is known), while OnToolExecutionEnd's ToolResultRecord
	// carries the REPAIRED ID and Name (the ones actually executed and
	// recorded). Correlate the pair by call order within the step, not by
	// ID, if RepairToolCall may rename calls.
	OnToolExecutionStart func(stepIndex int, call ToolCallRecord)
	OnToolExecutionEnd   func(stepIndex int, result ToolResultRecord, err error)

	// RuntimeContext, when set, is installed on the ctx passed to Tool.
	// Execute, ApprovalRequirer.ApprovalRequired, and ApproveToolCall for
	// this run — retrieve it with RuntimeContextFrom. It is installed once,
	// before the tool loop begins (both loops), so every step and every
	// resumed batch sees the same value.
	RuntimeContext RuntimeContext

	// ApproveToolCall decides approval-needing calls inline. Called once per
	// call whose Tool implements ApprovalRequirer and reports true from
	// ApprovalRequired, UNLESS Approvals already supplies a decision for
	// that call's ToolCallID (Approvals is checked first). Return
	// (decision, true) to decide; (zero, false) to leave the call pending,
	// which suspends the loop (see PendingApprovals). The ctx passed in
	// carries the same RuntimeContext installed for the run.
	ApproveToolCall func(ctx context.Context, req ApprovalRequest) (ApprovalDecision, bool)
	// Approvals supplies out-of-band decisions on a resume call, matched by
	// ToolCallID against the unanswered assistant tool-call batch at the end
	// of Messages (see Messages's resume semantics). It is consulted ONLY
	// for that resume batch — not for any approval-needing call arising
	// later within the same run, which must instead be decided via
	// ApproveToolCall (or left to suspend). This scoping exists because
	// Approvals matches by ToolCallID alone, and some providers (e.g.
	// geminicompat) synthesize deterministic IDs from a call's name and
	// index; a later batch's call can legitimately reuse an ID an earlier
	// Approvals entry already answered, and consulting Approvals for it too
	// would auto-approve a call no one actually decided.
	Approvals []ApprovalDecision
}

// ModelCallEnd is the argument to GenerateTextOpts.OnModelCallEnd — see that
// field's doc for how its fields are populated differently between
// GenerateText and StreamText.
type ModelCallEnd struct {
	StepIndex    int
	Response     *provider.Response // GenerateText only
	Usage        provider.Usage
	FinishReason provider.FinishReason
	Err          error
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
			Name:          t.Name(),
			Description:   t.Description(),
			Schema:        t.Schema(),
			Strict:        t.Strict(),
			InputExamples: t.InputExamples(),
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
		Reasoning:        opts.Reasoning,
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
