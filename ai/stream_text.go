package ai

import (
	"context"
	"iter"
	"time"

	"github.com/azrtydxb/go-ai-sdk/internal/retry"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// TextStream is the result of StreamText: a single-use iterator over the
// unified stream parts of a (possibly multi-step) tool-calling loop, plus
// accumulated results available after iteration completes.
type TextStream struct {
	ctx        context.Context
	opts       GenerateTextOpts
	maxRetries int
	maxSteps   int
	messages   []provider.Message
	model      provider.LanguageModel // current model; swappable via PrepareStep

	current provider.StreamResponse // the active provider stream

	activeTools map[string]bool // nil means all of opts.Tools are active

	started    bool
	closed     bool
	err        error
	abortFired bool // OnAbort has fired for this stream; fires at most once

	cancelTotal context.CancelFunc // releases Timeout.Total's derived ctx; always non-nil (no-op when Total unset)

	// callCtx is the exact context used for the CURRENT step's model
	// call/stream — the deepest of run ctx / Timeout.Step's derived ctx /
	// Timeout.Chunk's watchdog-derived ctx. timeoutErrorFor(s.callCtx, ...)
	// classifies a step's failure; stepCancel and chunkWD release/stop the
	// per-step resources once that step is done being read.
	callCtx    context.Context
	stepCancel context.CancelFunc
	chunkWD    *chunkWatchdog

	steps         []Step
	totalUsage    provider.Usage
	lastText      string
	lastReasoning string
	lastSources   []provider.SourcePart
	lastFinish    provider.FinishReason

	// pendingApprovals is set when the tool loop suspended because some
	// call(s) needed approval and none was available — either on the
	// resume batch (before any stream ran) or mid-loop, after a step. See
	// GenerateTextResult.PendingApprovals.
	pendingApprovals []ApprovalRequest
}

// StreamText starts the first model call (retried like GenerateText) and
// returns a *TextStream. A non-nil error means the stream could not start.
func StreamText(ctx context.Context, opts GenerateTextOpts) (*TextStream, error) {
	if opts.Output != nil {
		return nil, ErrOutputWithStreamText
	}

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

	// RuntimeContext is installed once, before the loop (and before the
	// resume batch, if any) — see GenerateTextOpts.RuntimeContext.
	ctx = withRuntimeContext(ctx, opts.RuntimeContext)

	// Timeout.Total, if set, bounds the whole run — see Timeout's doc. Its
	// cancel is released whenever this *TextStream's Parts() iteration ends
	// (deferred there) or Close() is called; both are safe to call
	// redundantly (context cancel funcs are idempotent).
	ctx, cancelTotal := withTotalTimeout(ctx, opts.Timeout)

	s := &TextStream{
		ctx:         ctx,
		opts:        opts,
		maxRetries:  maxRetries,
		maxSteps:    maxSteps,
		messages:    messages,
		model:       opts.Model,
		activeTools: activeToolSet(opts.ActiveTools),
		cancelTotal: cancelTotal,
	}

	// Resume: an unanswered assistant tool-call batch at the end of
	// Messages is run first — approval rules applied — before the first
	// model call. See GenerateTextOpts.Messages's resume-semantics doc.
	if resumeCalls := trailingUnansweredToolCalls(messages); len(resumeCalls) > 0 {
		batch, berr := runApprovalAwareToolCalls(ctx, opts, opts.Tools, resumeCalls, s.activeTools, 0, true)
		if berr != nil {
			// Mirrors the first-stream-start failure path below: no
			// *TextStream has been returned to a caller yet, so this is
			// reported solely via the returned error, not OnError.
			cancelTotal()
			return nil, berr
		}
		if len(batch.pending) > 0 {
			// Nothing has streamed yet — Parts() will observe s.current ==
			// nil and finish immediately, delivering PendingApprovals via
			// the result surface (see Parts and buildResult).
			s.pendingApprovals = batch.pending
			s.lastFinish = provider.FinishToolCalls
			return s, nil
		}
		s.messages = append(s.messages, toolResultMessage(batch.results))
	}

	call.Messages = s.messages
	if opts.PrepareStep != nil {
		if plan, ok := opts.PrepareStep(0, StepPlan{Call: call, Model: s.model}); ok {
			call = plan.Call
			if plan.Model != nil {
				s.model = plan.Model
			}
		}
	}

	if opts.OnModelCallStart != nil {
		opts.OnModelCallStart(0, call)
	}
	stream, callCtx, cancelStep, wd, err := startStepStream(ctx, opts.Timeout, s.model, call, call.Messages, maxRetries)
	if err != nil {
		// A step (or total) timeout firing here is OUR bound, not a user
		// abort — surfaced as *TimeoutError in place of the raw ctx/retry
		// error, exactly like GenerateText's per-step handling. A genuine
		// caller ctx cancellation leaves err unchanged.
		if te, ok := timeoutErrorFor(callCtx, opts.Timeout); ok {
			err = te
		}
		// No *TextStream has been returned to a caller yet at this point,
		// so there is no possibility of the OnAbort path applying here (it
		// requires an existing TextStream) — unlike the in-loop failures
		// below, this End always fires unconditionally on failure. On
		// success, End does NOT fire here: it fires later, from Parts(),
		// once step 0's FinishPart is actually observed.
		if opts.OnModelCallEnd != nil {
			opts.OnModelCallEnd(ModelCallEnd{StepIndex: 0, Err: err})
		}
		wd.Stop()
		cancelStep()
		cancelTotal()
		return nil, err
	}
	s.current = stream
	s.callCtx = callCtx
	s.stepCancel = cancelStep
	s.chunkWD = wd

	return s, nil
}

// startStepStream starts a step's stream (through retry), deriving a
// step-scoped context from runCtx that applies Timeout's Step bound and, if
// Timeout.Chunk is set, arms a chunk-stall watchdog around it (see
// chunkWatchdog). It returns the stream, the EXACT context used to make the
// request — the deepest of runCtx/Step's/Chunk's derived contexts, which is
// what timeoutErrorFor must be checked against to classify a later failure —
// that context's cancel func, and the watchdog (nil if Timeout.Chunk is
// unset). The caller must Reset() the watchdog on every yielded stream part
// and Stop() both the watchdog and the returned cancel func once the step is
// done being read (success, error, or abandoned iteration) — both are
// nil/no-op-safe when Timeout (or the relevant field) is unset.
func startStepStream(runCtx context.Context, t *Timeout, model provider.LanguageModel, call provider.Call, messages []provider.Message, maxRetries int) (provider.StreamResponse, context.Context, context.CancelFunc, *chunkWatchdog, error) {
	stepCtx, cancelStep := withStepTimeout(runCtx, t)
	var chunk time.Duration
	if t != nil {
		chunk = t.Chunk
	}
	callCtx, wd := newChunkWatchdog(stepCtx, chunk)
	stream, err := startStream(callCtx, model, call, messages, maxRetries)
	return stream, callCtx, cancelStep, wd, err
}

// startStream begins a model stream (through retry) using messages as the
// call's message list.
func startStream(ctx context.Context, model provider.LanguageModel, call provider.Call, messages []provider.Message, maxRetries int) (provider.StreamResponse, error) {
	call.Messages = messages
	stream, err := retry.Do(ctx, maxRetries, func() (provider.StreamResponse, error) {
		return model.Stream(ctx, call)
	})
	if err := translateRetryErr(err); err != nil {
		return nil, err
	}
	return stream, nil
}

// Parts yields unified parts across ALL steps of the tool loop: TextDelta,
// ToolCallDelta, ToolCallEnd, and one FinishPart per step. Between steps it
// executes any requested tools and starts the next model stream. Iteration
// is single-use: calling Parts() again after exhausting (or abandoning) it
// yields nothing.
func (s *TextStream) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		if s.started {
			return
		}
		s.started = true
		// Releases Timeout.Total's derived ctx (a no-op if Total is unset)
		// once this Parts() call ends, however it ends — natural finish,
		// error, or abandoned iteration.
		defer s.cancelTotal()

		for {
			stream := s.current
			if stream == nil {
				// Reached when the resume batch (see StreamText) suspended
				// before any model call was ever made: there is no step to
				// emit parts for, so finish immediately (OnFinish still
				// fires, carrying PendingApprovals) when that's why we're
				// here; otherwise (nothing left to do, and nothing was ever
				// pending) this is a no-op, matching the pre-approvals
				// behavior. finishOrTimeout (not finish) because the resume
				// batch's tool execution, in StreamText, ran before any
				// *TextStream existed to check ctx against.
				if len(s.pendingApprovals) > 0 && len(s.steps) == 0 {
					s.finishOrTimeout()
				}
				return
			}
			stepIndex := len(s.steps)

			var text string
			var reasoningText string
			// reasoningTail accumulates ReasoningDelta text received since
			// the last ReasoningEnd (or since the start of the step, if no
			// ReasoningEnd has occurred yet), and is reset to empty at every
			// ReasoningEnd. This lets step assembly below tell a second,
			// still-open reasoning span (deltas that arrived after the last
			// ReasoningEnd, with no closing ReasoningEnd of their own before
			// the step's FinishPart) apart from the already-closed spans
			// captured in reasoningParts, so its text isn't silently
			// dropped from the assembled step.
			var reasoningTail string
			var reasoningParts []provider.ReasoningPart
			var sources []provider.SourcePart
			var toolCalls []provider.ToolCallPart
			type pendingCall struct {
				name       string
				args       []byte
				startFired bool
			}
			argsByID := map[string]*pendingCall{}
			toolsByName := buildToolNameMap(s.opts.Tools)
			var finish provider.FinishPart
			var gotFinish bool
			abandoned := false

			for p := range stream.Parts() {
				// Timeout.Chunk's watchdog resets on every yielded part — a
				// stall (no part within Chunk) cancels s.callCtx with
				// errChunkTimeout, which unblocks/errors the underlying
				// provider stream (its Parts() range ends, normally via
				// stream.Err() below).
				s.chunkWD.Reset()
				if s.opts.OnChunk != nil {
					s.opts.OnChunk(p)
				}
				switch part := p.(type) {
				case provider.TextDelta:
					text += part.Text
				case provider.ReasoningDelta:
					reasoningText += part.Text
					reasoningTail += part.Text
				case provider.ReasoningEnd:
					reasoningParts = append(reasoningParts, part.Part)
					reasoningTail = ""
				case provider.SourceEvent:
					sources = append(sources, part.Source)
				case provider.ToolCallDelta:
					pc, ok := argsByID[part.ID]
					if !ok {
						pc = &pendingCall{}
						argsByID[part.ID] = pc
					}
					if part.Name != "" {
						pc.name = part.Name
					}
					pc.args = append(pc.args, part.ArgsDelta...)

					// OnInputStart/OnInputDelta are stream-only (no
					// GenerateText equivalent — see ToolInputCallbacks) and
					// keyed by toolCallID; the tool is matched by name, which
					// may arrive on a later delta than the first for a given
					// ID (see ToolCallDelta.Name's doc), so OnInputStart can
					// only fire once the name is known. A delta that arrives
					// before the name is known (or names a tool the run
					// doesn't have) is silently skipped rather than buffered
					// for a later replay — matching how the fallback
					// ToolCallPart assembly below already tolerates a
					// same-ID delta stream with no matching tool.
					if pc.name != "" {
						if t, ok := toolsByName[pc.name]; ok {
							cb := t.InputCallbacks()
							if !pc.startFired {
								pc.startFired = true
								if cb.OnInputStart != nil {
									cb.OnInputStart(s.ctx, part.ID)
								}
							}
							if cb.OnInputDelta != nil {
								cb.OnInputDelta(s.ctx, part.ID, part.ArgsDelta)
							}
						}
					}
				case provider.ToolCallEnd:
					toolCalls = append(toolCalls, part.Call)
				case provider.FinishPart:
					finish = part
					gotFinish = true
				}

				if !yield(p) {
					abandoned = true
					break
				}
			}

			// The step's model-call phase is over either way past this
			// point (abandoned, error, or success) — release Timeout.Step's
			// and Timeout.Chunk's per-step resources now so they never
			// outlive the step they were created for (no goroutine/timer
			// leak). Both are nil/no-op-safe when unset.
			s.chunkWD.Stop()
			s.stepCancel()

			if abandoned {
				s.fireAbort()
				_ = stream.Close()
				s.current = nil
				return
			}

			if err := stream.Err(); err != nil {
				// A step/chunk/total timeout firing here is OUR bound, not a
				// user abort — surfaced as *TimeoutError so
				// reportAbortOrError/fireModelCallEnd route it to OnError
				// rather than OnAbort (see their doc comments and
				// timeoutErrorFor).
				if te, ok := timeoutErrorFor(s.callCtx, s.opts.Timeout); ok {
					err = te
				}
				s.err = err
				_ = stream.Close()
				s.current = nil
				s.fireModelCallEnd(ModelCallEnd{StepIndex: stepIndex, Err: err})
				s.reportAbortOrError(err)
				return
			}
			_ = stream.Close()
			s.fireModelCallEnd(ModelCallEnd{StepIndex: stepIndex, Usage: finish.Usage, FinishReason: finish.Reason})

			// Fill in any tool calls that only arrived as deltas (no
			// ToolCallEnd), using the accumulated ArgsDelta as fallback.
			seen := make(map[string]bool, len(toolCalls))
			for _, tc := range toolCalls {
				seen[tc.ID] = true
			}
			for id, pc := range argsByID {
				if seen[id] {
					continue
				}
				toolCalls = append(toolCalls, provider.ToolCallPart{ID: id, Name: pc.name, Args: pc.args})
			}

			// Assemble the step's Response content, mirroring how tool
			// calls are assembled from ToolCallEnd/deltas above:
			// providers that emit a fully assembled ReasoningEnd (e.g.
			// Anthropic, carrying a signature) supply reasoningParts
			// directly; providers that only ever emit ReasoningDelta text
			// (e.g. openaicompat reasoning_content), with no ReasoningEnd at
			// all, get a single synthesized ReasoningPart from the
			// accumulated text. When ReasoningEnd(s) DID occur but more
			// ReasoningDelta text arrived afterward (a second, still-open
			// span with no closing ReasoningEnd before the step ended),
			// reasoningTail holds that uncovered trailing text — append it
			// as an additional synthesized ReasoningPart so it isn't
			// silently dropped from the assembled step.
			var respContent []provider.ContentPart
			if len(reasoningParts) > 0 {
				for _, rp := range reasoningParts {
					respContent = append(respContent, rp)
				}
				if reasoningTail != "" {
					respContent = append(respContent, provider.ReasoningPart{Text: reasoningTail})
				}
			} else if reasoningText != "" {
				respContent = append(respContent, provider.ReasoningPart{Text: reasoningText})
			}
			if text != "" {
				respContent = append(respContent, provider.TextPart{Text: text})
			}
			for _, sp := range sources {
				respContent = append(respContent, sp)
			}
			for _, tc := range toolCalls {
				respContent = append(respContent, tc)
			}
			stepResp := &provider.Response{
				Content:          respContent,
				FinishReason:     finish.Reason,
				Usage:            finish.Usage,
				ProviderMetadata: finish.ProviderMetadata,
			}

			step := Step{
				Text:          text,
				ReasoningText: stepResp.ReasoningText(),
				Sources:       stepResp.SourceParts(),
				FinishReason:  finish.Reason,
				Usage:         finish.Usage,
				Response:      stepResp,
			}
			for _, tc := range toolCalls {
				step.ToolCalls = append(step.ToolCalls, ToolCallRecord{ID: tc.ID, Name: tc.Name, Args: tc.Args})
			}

			s.totalUsage.InputTokens += finish.Usage.InputTokens
			s.totalUsage.OutputTokens += finish.Usage.OutputTokens
			s.totalUsage.TotalTokens += finish.Usage.TotalTokens
			s.totalUsage.CachedInputTokens += finish.Usage.CachedInputTokens
			s.totalUsage.ReasoningTokens += finish.Usage.ReasoningTokens
			s.lastText = text
			s.lastReasoning = step.ReasoningText
			s.lastSources = step.Sources
			if gotFinish {
				s.lastFinish = finish.Reason
			}

			s.messages = append(s.messages, provider.Message{Role: provider.RoleAssistant, Content: assistantContent(respContent)})

			hasToolCalls := len(toolCalls) > 0

			if hasToolCalls {
				batch, err := runApprovalAwareToolCalls(s.ctx, s.opts, s.opts.Tools, toolCalls, s.activeTools, stepIndex, false)
				if err != nil {
					s.err = err
					step.ToolResults = nil
					s.steps = append(s.steps, step)
					s.current = nil
					s.reportAbortOrError(err)
					return
				}
				if len(batch.pending) > 0 {
					// Batch atomicity: nothing in this batch executed. The
					// step is still recorded (tool-result-less) and
					// OnStepFinish still fires, but the loop stops here —
					// see GenerateTextResult.PendingApprovals.
					s.pendingApprovals = batch.pending
					s.steps = append(s.steps, step)
					if s.opts.OnStepFinish != nil {
						s.opts.OnStepFinish(step)
					}
					s.current = nil
					s.finishOrTimeout()
					return
				}
				step.ToolResults = batch.results
				s.messages = append(s.messages, toolResultMessage(batch.results))
			}

			s.steps = append(s.steps, step)

			if s.opts.OnStepFinish != nil {
				s.opts.OnStepFinish(step)
			}

			// StopWhen is consulted after every step — see
			// GenerateTextOpts.StopWhen — even though, as in GenerateText,
			// that doesn't change when the loop actually stops for a
			// no-tool-call step (already handled by the hasToolCalls check
			// below).
			stopByCondition := s.opts.StopWhen != nil && s.opts.StopWhen(s.steps)

			if !hasToolCalls {
				s.current = nil
				s.finishOrTimeout()
				return
			}
			if len(s.steps) >= s.maxSteps {
				s.current = nil
				s.finishOrTimeout()
				return
			}
			if stopByCondition {
				s.current = nil
				s.finishOrTimeout()
				return
			}

			call, err := buildCall(s.opts)
			if err != nil {
				s.err = err
				s.current = nil
				s.reportAbortOrError(err)
				return
			}
			call.Messages = s.messages
			if s.opts.PrepareStep != nil {
				if plan, ok := s.opts.PrepareStep(len(s.steps), StepPlan{Call: call, Model: s.model}); ok {
					call = plan.Call
					if plan.Model != nil {
						s.model = plan.Model
					}
				}
			}
			nextStepIndex := len(s.steps)
			if s.opts.OnModelCallStart != nil {
				s.opts.OnModelCallStart(nextStepIndex, call)
			}
			next, callCtx, cancelStep, wd, err := startStepStream(s.ctx, s.opts.Timeout, s.model, call, call.Messages, s.maxRetries)
			if err != nil {
				if te, ok := timeoutErrorFor(callCtx, s.opts.Timeout); ok {
					err = te
				}
				s.err = err
				s.current = nil
				s.fireModelCallEnd(ModelCallEnd{StepIndex: nextStepIndex, Err: err})
				s.reportAbortOrError(err)
				wd.Stop()
				cancelStep()
				return
			}
			s.current = next
			s.callCtx = callCtx
			s.stepCancel = cancelStep
			s.chunkWD = wd
		}
	}
}

// buildToolNameMap indexes tools by name, unfiltered by active-tool status:
// used solely to look up a tool's ToolInputCallbacks by the name carried on
// a ToolCallDelta, which is a streaming-lifecycle concern orthogonal to
// whether the tool is currently active (see activeToolSet).
func buildToolNameMap(tools []Tool) map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return m
}

// assistantContent returns a defensive copy of an assembled step's content
// parts (reasoning, text, tool calls, in that order) for the assistant
// message appended to the conversation.
func assistantContent(respContent []provider.ContentPart) []provider.ContentPart {
	if len(respContent) == 0 {
		return nil
	}
	return append([]provider.ContentPart(nil), respContent...)
}

// reportAbortOrError classifies a terminal error occurring anywhere in the
// tool loop — a step's stream failing, tool execution failing, or a
// subsequent step's call failing to build or start — and dispatches it to
// exactly one of OnAbort/OnError: if s.ctx is canceled (or its deadline has
// passed) at the time of this check, err is treated as caused by that
// cancellation and fireAbort runs instead of OnError, mirroring the
// mutual-exclusivity GenerateTextOpts.OnAbort's doc comment describes for
// the original mid-stream-error case; otherwise OnError runs (if set).
//
// This ctx check happens once, after the fact, rather than at the moment
// err actually occurred — so it is inherently a classification-by-timing,
// not a proof of causation: a genuine provider failure that happens to race
// a ctx cancellation arriving at (almost) the same instant is classified as
// an abort too, since there is no way to distinguish "canceled because of
// this error" from "canceled independently, moments before this error was
// observed" from the information available here.
//
// Timeout carve-out: s.ctx is the RUN-level context — it only ever becomes
// Done due to the caller's own ctx being canceled/expiring, or Timeout.Total
// firing (Total's derived context IS s.ctx; see StreamText). A Step or Chunk
// bound firing cancels a deeper, per-step context instead (s.callCtx) and
// never touches s.ctx, so it can never reach this check at all — by the time
// err reaches here it has already been substituted with the corresponding
// *TimeoutError by the call site (see the stream.Err() and next-step-start
// branches in Parts()), and falls straight through to the OnError call
// below. When s.ctx IS Done, timeoutErrorFor tells apart the two remaining
// cases: our own Total bound (falls through to OnError, same as
// Step/Chunk — an SDK-imposed limit is an error, not a user abort) from the
// caller's own ctx (fireAbort, unchanged from before Timeout existed).
func (s *TextStream) reportAbortOrError(err error) {
	if s.ctx.Err() != nil {
		if _, ok := timeoutErrorFor(s.ctx, s.opts.Timeout); !ok {
			s.fireAbort()
			return
		}
	}
	if s.opts.OnError != nil {
		s.opts.OnError(err)
	}
}

// fireModelCallEnd invokes opts.OnModelCallEnd, if set, for a step's model
// call within the tool loop (i.e. everywhere except the very first call,
// which StreamText itself handles before any *TextStream exists to abort).
// When end.Err is non-nil AND s.ctx is canceled, it does NOT fire — that
// termination is instead reported solely via OnAbort (see
// GenerateTextOpts.OnModelCallEnd and OnAbort), mirroring the same
// ctx-cancellation check reportAbortOrError performs for OnError. When
// end.Err is nil (the step's model call succeeded), it always fires.
//
// The same Timeout carve-out reportAbortOrError documents applies here: when
// s.ctx is Done because of our OWN Total bound (not the caller's ctx), this
// still fires — only a genuine caller-ctx abort suppresses it.
func (s *TextStream) fireModelCallEnd(end ModelCallEnd) {
	if s.opts.OnModelCallEnd == nil {
		return
	}
	if end.Err != nil && s.ctx.Err() != nil {
		if _, ok := timeoutErrorFor(s.ctx, s.opts.Timeout); !ok {
			return
		}
	}
	s.opts.OnModelCallEnd(end)
}

// fireAbort invokes opts.OnAbort, if set, the first time it is called for
// this stream; subsequent calls are no-ops. See GenerateTextOpts.OnAbort.
func (s *TextStream) fireAbort() {
	if s.abortFired {
		return
	}
	s.abortFired = true
	if s.opts.OnAbort != nil {
		s.opts.OnAbort()
	}
}

// finish invokes opts.OnFinish, if set, with a *GenerateTextResult built
// from the stream's accumulated state. Called only at a natural end of
// Parts() iteration (no error, not abandoned early).
func (s *TextStream) finish() {
	if s.opts.OnFinish == nil {
		return
	}
	s.opts.OnFinish(s.buildResult())
}

// finishOrTimeout is what every natural-end point in Parts() calls instead
// of finish() directly: a step's tool execution (runApprovalAwareToolCalls)
// can take arbitrarily long, and — unlike a step's model call/stream, which
// is always followed by a ctx check before the next one starts — nothing
// else checks ctx between tool execution finishing and a natural-end return
// (no more tool calls, MaxSteps reached, StopWhen true, or a pending-approval
// suspension). Without this check, OUR Total bound elapsing during that
// window would otherwise be reported as silent success: s.err nil, OnFinish
// firing normally, with the breach visible only buried in a
// ToolResultRecord.Err.
//
// If s.ctx is done because OUR Total sentinel fired, this substitutes a
// *TimeoutError and reports it via OnError (never OnFinish) — an
// SDK-imposed limit is an error, not a user abort. If s.ctx is done for any
// OTHER reason (the caller's own ctx canceled or reaching its own deadline),
// this fires OnAbort instead — the same distinction reportAbortOrError
// makes for a mid-stream error, applied here so a genuine user cancel during
// this window is classified identically wherever it's observed. Only when
// s.ctx is not done at all does this proceed to the normal success path.
func (s *TextStream) finishOrTimeout() {
	if s.ctx.Err() != nil {
		if te, ok := timeoutErrorFor(s.ctx, s.opts.Timeout); ok {
			s.err = te
			if s.opts.OnError != nil {
				s.opts.OnError(te)
			}
			return
		}
		s.fireAbort()
		return
	}
	s.finish()
}

// buildResult assembles a *GenerateTextResult from the stream's accumulated
// steps/usage/messages, mirroring the shape GenerateText returns for the
// same underlying model script.
func (s *TextStream) buildResult() *GenerateTextResult {
	result := &GenerateTextResult{
		Steps:            append([]Step(nil), s.steps...),
		Usage:            s.totalUsage,
		Messages:         s.messages,
		FinishReason:     s.lastFinish,
		PendingApprovals: s.pendingApprovals,
	}
	if len(s.steps) > 0 {
		last := s.steps[len(s.steps)-1]
		result.Text = last.Text
		result.ReasoningText = last.ReasoningText
		result.Sources = last.Sources
		result.ToolCalls = last.ToolCalls
		result.ToolResults = last.ToolResults
		result.FinishReason = last.FinishReason
	}
	return result
}

// Err returns the error, if any, that ended iteration abnormally: a
// *RetryError if a subsequent step's stream could not start, a
// *NoSuchToolError if an unknown tool was requested, or the underlying
// provider stream's mid-stream error.
func (s *TextStream) Err() error { return s.err }

// Text returns the accumulated text of the final step.
func (s *TextStream) Text() string { return s.lastText }

// ReasoningText returns the accumulated reasoning text of the final step.
func (s *TextStream) ReasoningText() string { return s.lastReasoning }

// Sources returns the SourceParts accumulated (via SourceEvent stream
// parts) during the final step.
func (s *TextStream) Sources() []provider.SourcePart { return s.lastSources }

// Steps returns the steps executed so far. If iteration stopped because of a
// *NoSuchToolError (an unknown tool was requested), the step in which that
// happened is still appended, with its ToolCalls populated but ToolResults
// nil (execution never ran) — check Err() to detect this case rather than
// assuming every step in Steps() completed successfully.
func (s *TextStream) Steps() []Step { return s.steps }

// Usage returns the summed usage across all steps.
func (s *TextStream) Usage() provider.Usage { return s.totalUsage }

// FinishReason returns the last step's finish reason.
func (s *TextStream) FinishReason() provider.FinishReason { return s.lastFinish }

// Messages returns the full final conversation so far, including any
// assistant and tool messages appended by completed steps of the tool loop
// — the same semantics as GenerateTextResult.Messages. Valid after Parts()
// has been iterated (fully or partially); before that it is just the
// initial request messages.
func (s *TextStream) Messages() []provider.Message { return s.messages }

// PendingApprovals returns the same value as GenerateTextResult.
// PendingApprovals would carry, for a stream that suspended because some
// tool call(s) needed approval and none was available — see
// ApprovalRequirer and GenerateTextOpts.ApproveToolCall/Approvals. Nil when
// the stream never suspended. Valid after Parts() has been iterated (fully
// or partially, including the immediate-suspension case where Parts()
// yields nothing at all because the resumed batch itself was pending).
func (s *TextStream) PendingApprovals() []ApprovalRequest { return s.pendingApprovals }

// Close releases the underlying provider stream, if one is still open. It
// is idempotent and safe to call at any point: before Parts() has ever been
// ranged over (the caller decided not to consume the stream, so the HTTP
// body would otherwise leak), after Parts() has been fully iterated or
// abandoned (Parts() already closes the stream itself in both cases, so
// Close() is then a no-op), or mid-iteration. Close is not safe for
// concurrent use with Parts().
func (s *TextStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	// Releases Timeout's derived contexts/timers regardless of whether
	// Parts() was ever called (its own defer covers the case where it was;
	// these are idempotent/no-op-safe otherwise) — no goroutine/timer must
	// outlive a stream the caller is done with.
	s.chunkWD.Stop()
	if s.stepCancel != nil {
		s.stepCancel()
	}
	if s.cancelTotal != nil {
		s.cancelTotal()
	}
	if s.current == nil {
		return nil
	}
	err := s.current.Close()
	s.current = nil
	return err
}
