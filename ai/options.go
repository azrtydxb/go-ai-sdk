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
	MaxSteps      int  // default 1
	MaxRetries    *int // default 2
	MaxTokens     *int
	Temperature   *float64
	TopP          *float64
	StopSequences []string

	// ProviderOptions carries provider-specific escape-hatch parameters. It
	// is threaded through to provider.Call.ProviderOptions unchanged — see
	// that field's doc for the keying and merge semantics.
	ProviderOptions map[string]any
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
