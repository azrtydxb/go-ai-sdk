package ai

import (
	"context"
	"errors"
	"iter"

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

	current provider.StreamResponse // the active provider stream

	started bool
	err     error

	steps      []Step
	totalUsage provider.Usage
	lastText   string
	lastFinish provider.FinishReason
}

// StreamText starts the first model call (retried like GenerateText) and
// returns a *TextStream. A non-nil error means the stream could not start.
func StreamText(ctx context.Context, opts GenerateTextOpts) (*TextStream, error) {
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
	}

	messages := append([]provider.Message(nil), call.Messages...)

	s := &TextStream{
		ctx:        ctx,
		opts:       opts,
		maxRetries: maxRetries,
		maxSteps:   maxSteps,
		messages:   messages,
	}

	stream, err := startStream(ctx, opts.Model, call, messages, maxRetries)
	if err != nil {
		return nil, err
	}
	s.current = stream

	return s, nil
}

// startStream begins a model stream (through retry) using messages as the
// call's message list.
func startStream(ctx context.Context, model provider.LanguageModel, call provider.Call, messages []provider.Message, maxRetries int) (provider.StreamResponse, error) {
	call.Messages = messages
	stream, err := retry.Do(ctx, maxRetries, func() (provider.StreamResponse, error) {
		return model.Stream(ctx, call)
	})
	if err != nil {
		var exhausted *retry.ExhaustedError
		if errors.As(err, &exhausted) {
			return nil, &RetryError{Attempts: exhausted.Attempts, LastErr: exhausted.LastErr}
		}
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

		for {
			stream := s.current
			if stream == nil {
				return
			}

			var text string
			var toolCalls []provider.ToolCallPart
			argsByID := map[string]*[]byte{}
			var finish provider.FinishPart
			var gotFinish bool
			abandoned := false

			for p := range stream.Parts() {
				switch part := p.(type) {
				case provider.TextDelta:
					text += part.Text
				case provider.ToolCallDelta:
					buf, ok := argsByID[part.ID]
					if !ok {
						b := []byte{}
						buf = &b
						argsByID[part.ID] = buf
					}
					*buf = append(*buf, part.ArgsDelta...)
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

			if abandoned {
				_ = stream.Close()
				s.current = nil
				return
			}

			if err := stream.Err(); err != nil {
				s.err = err
				_ = stream.Close()
				s.current = nil
				return
			}
			_ = stream.Close()

			// Fill in any tool calls that only arrived as deltas (no
			// ToolCallEnd), using the accumulated ArgsDelta as fallback.
			seen := make(map[string]bool, len(toolCalls))
			for _, tc := range toolCalls {
				seen[tc.ID] = true
			}
			for id, buf := range argsByID {
				if seen[id] {
					continue
				}
				toolCalls = append(toolCalls, provider.ToolCallPart{ID: id, Args: *buf})
			}

			step := Step{
				Text:         text,
				FinishReason: finish.Reason,
				Usage:        finish.Usage,
			}
			for _, tc := range toolCalls {
				step.ToolCalls = append(step.ToolCalls, ToolCallRecord{ID: tc.ID, Name: tc.Name, Args: tc.Args})
			}

			s.totalUsage.InputTokens += finish.Usage.InputTokens
			s.totalUsage.OutputTokens += finish.Usage.OutputTokens
			s.totalUsage.TotalTokens += finish.Usage.TotalTokens
			s.lastText = text
			if gotFinish {
				s.lastFinish = finish.Reason
			}

			s.messages = append(s.messages, provider.Message{Role: provider.RoleAssistant, Content: assistantContent(text, toolCalls)})

			hasToolCalls := len(toolCalls) > 0

			if hasToolCalls {
				results, err := runToolCalls(s.ctx, s.opts.Tools, toolCalls)
				if err != nil {
					s.err = err
					step.ToolResults = nil
					s.steps = append(s.steps, step)
					s.current = nil
					return
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
				s.messages = append(s.messages, provider.Message{Role: provider.RoleTool, Content: resultParts})
			}

			s.steps = append(s.steps, step)

			if !hasToolCalls || len(s.steps) >= s.maxSteps {
				s.current = nil
				return
			}

			call, err := buildCall(s.opts)
			if err != nil {
				s.err = err
				s.current = nil
				return
			}
			next, err := startStream(s.ctx, s.opts.Model, call, s.messages, s.maxRetries)
			if err != nil {
				s.err = err
				s.current = nil
				return
			}
			s.current = next
		}
	}
}

// assistantContent converts an assembled step's text and tool calls into
// content parts for the assistant message appended to the conversation.
func assistantContent(text string, toolCalls []provider.ToolCallPart) []provider.ContentPart {
	var parts []provider.ContentPart
	if text != "" {
		parts = append(parts, provider.TextPart{Text: text})
	}
	for _, tc := range toolCalls {
		parts = append(parts, tc)
	}
	return parts
}

// Err returns the error, if any, that ended iteration abnormally: a
// *RetryError if a subsequent step's stream could not start, a
// *NoSuchToolError if an unknown tool was requested, or the underlying
// provider stream's mid-stream error.
func (s *TextStream) Err() error { return s.err }

// Text returns the accumulated text of the final step.
func (s *TextStream) Text() string { return s.lastText }

// Steps returns the steps executed so far.
func (s *TextStream) Steps() []Step { return s.steps }

// Usage returns the summed usage across all steps.
func (s *TextStream) Usage() provider.Usage { return s.totalUsage }

// FinishReason returns the last step's finish reason.
func (s *TextStream) FinishReason() provider.FinishReason { return s.lastFinish }
