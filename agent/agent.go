// Package agent provides a reusable, higher-level configuration on top of
// package ai's GenerateText/StreamText: an Agent bundles a model, system
// instructions, tools, and a handful of loop-shaping options (MaxSteps,
// StopWhen, Output, RuntimeContext, ApproveToolCall) into one value that can
// be run repeatedly with different inputs via RunOpts, and exposed to
// another Agent as a tool via AsTool.
//
// Agent contains no loop logic of its own — Generate and Stream simply
// assemble an ai.GenerateTextOpts from the Agent's fields and the given
// RunOpts, apply PrepareOpts if set, and delegate to ai.GenerateText /
// ai.StreamText. All the tool-calling, retry, and streaming semantics
// documented on those functions apply unchanged.
//
// Use raw ai.GenerateText/ai.StreamText directly for a one-off call whose
// options don't need to be reused. Reach for Agent when the same
// model+instructions+tools configuration will run more than once — e.g. a
// chat loop, a sub-agent delegated to via AsTool, or anywhere the call site
// shouldn't have to re-specify Tools/Instructions/MaxSteps every time.
package agent

import (
	"context"
	"errors"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultMaxSteps is the MaxSteps applied when Agent.MaxSteps is 0. Unlike
// ai.GenerateTextOpts, whose MaxSteps defaults to 1 (a single model call,
// no tool loop, unless StopWhen is set — see its doc), an Agent is meant to
// run a multi-step tool-calling loop by default, so it defaults to 8
// instead.
const defaultMaxSteps = 8

// Agent is a reusable configuration for running a model+tools loop — the
// go-ai-sdk equivalent of the AI SDK's ToolLoopAgent. Construct one with the
// fields below set, then call Generate or Stream (any number of times, with
// different RunOpts) to execute it.
type Agent struct {
	// Model is the language model the agent runs against. Required.
	Model provider.LanguageModel
	// Instructions is the system prompt, passed through as
	// ai.GenerateTextOpts.System.
	Instructions string
	// Tools are the tools offered to the model on every run.
	Tools []ai.Tool
	// MaxSteps caps the number of steps in the tool-calling loop. Zero
	// means defaultMaxSteps (8) — see its doc for how this differs from
	// ai.GenerateTextOpts's own default.
	MaxSteps int
	// StopWhen, when set, is passed through to
	// ai.GenerateTextOpts.StopWhen unchanged.
	StopWhen func([]ai.Step) bool
	// Output, when set, selects a structured-output mode for the run — see
	// ai.Output and ai.OutputObject/OutputArray/OutputChoice/OutputJSON.
	Output ai.Output
	// RuntimeContext is installed on the ctx passed to this agent's tools
	// (and to ApproveToolCall/ApprovalRequired) for the duration of each
	// run — see ai.RuntimeContext. Nil is a no-op, not a clear: per
	// ai.RuntimeContextFrom, a nil RuntimeContext leaves whatever
	// RuntimeContext was already installed on ctx untouched. This matters
	// for an Agent run via AsTool as a sub-agent: with RuntimeContext left
	// unset, the sub-agent's tools inherit the PARENT agent's
	// RuntimeContext (already on ctx when the parent's tool loop calls
	// this tool's Execute); setting RuntimeContext to any non-nil value
	// (including an explicitly empty ai.RuntimeContext{}) overrides that
	// inheritance and installs/shadows it for this agent's own run instead
	// — see AsTool's doc for the full sub-agent scoping rule.
	RuntimeContext ai.RuntimeContext
	// ApproveToolCall, when set, is passed through to
	// ai.GenerateTextOpts.ApproveToolCall unchanged.
	ApproveToolCall func(ctx context.Context, req ai.ApprovalRequest) (ai.ApprovalDecision, bool)
	// PrepareOpts, when set, receives the fully-assembled GenerateTextOpts
	// before each run for arbitrary customization (settings, callbacks,
	// ProviderOptions). It runs last — after every other field above has
	// been applied to opts — so whatever it sets on opts wins.
	PrepareOpts func(opts *ai.GenerateTextOpts)
}

// RunOpts is one invocation of an Agent. Exactly one of Prompt/Messages must
// be set — validation is delegated to ai.GenerateText/ai.StreamText, which
// return their own error for both-or-neither (see ai.GenerateTextOpts).
type RunOpts struct {
	// Prompt is a single user-turn prompt. Exactly one of Prompt/Messages.
	Prompt string
	// Messages is a full conversation, e.g. to continue a prior run or
	// pass multi-turn history. Exactly one of Prompt/Messages.
	Messages []provider.Message
	// Approvals resolves previously-pending tool-call approvals, passed
	// through to ai.GenerateTextOpts.Approvals unchanged — see
	// ai.GenerateTextResult.PendingApprovals for the resume flow.
	Approvals []ai.ApprovalDecision
}

// buildOpts assembles the ai.GenerateTextOpts for one run of a, applying run
// and then a.PrepareOpts (which runs last and wins) on top.
func (a *Agent) buildOpts(run RunOpts) ai.GenerateTextOpts {
	maxSteps := a.MaxSteps
	if maxSteps == 0 {
		maxSteps = defaultMaxSteps
	}

	opts := ai.GenerateTextOpts{
		Model:           a.Model,
		System:          a.Instructions,
		Prompt:          run.Prompt,
		Messages:        run.Messages,
		Tools:           a.Tools,
		MaxSteps:        maxSteps,
		StopWhen:        a.StopWhen,
		Output:          a.Output,
		RuntimeContext:  a.RuntimeContext,
		ApproveToolCall: a.ApproveToolCall,
		Approvals:       run.Approvals,
	}

	if a.PrepareOpts != nil {
		a.PrepareOpts(&opts)
	}

	return opts
}

// Generate runs the agent once against run, delegating entirely to
// ai.GenerateText.
func (a *Agent) Generate(ctx context.Context, run RunOpts) (*ai.GenerateTextResult, error) {
	if a == nil {
		return nil, errors.New("agent: nil Agent")
	}
	return ai.GenerateText(ctx, a.buildOpts(run))
}

// Stream runs the agent once against run, delegating entirely to
// ai.StreamText. If Output is set, ai.StreamText's own restriction applies
// unchanged: it returns ai.ErrOutputWithStreamText immediately rather than
// streaming — Stream does not intercept or drop that error, it passes it
// straight through to the caller.
func (a *Agent) Stream(ctx context.Context, run RunOpts) (*ai.TextStream, error) {
	if a == nil {
		return nil, errors.New("agent: nil Agent")
	}
	return ai.StreamText(ctx, a.buildOpts(run))
}
