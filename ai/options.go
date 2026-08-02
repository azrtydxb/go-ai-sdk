package ai

import (
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

	// ProviderOptions carries provider-specific escape-hatch parameters. It
	// is threaded through to provider.Call.ProviderOptions unchanged — see
	// that field's doc for the keying and merge semantics.
	ProviderOptions map[string]any
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

	var toolDefs []provider.ToolDef
	for _, t := range opts.Tools {
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
