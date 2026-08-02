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
	closed  bool
	err     error

	steps         []Step
	totalUsage    provider.Usage
	lastText      string
	lastReasoning string
	lastFinish    provider.FinishReason
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
	} else if opts.StopWhen != nil {
		maxSteps = defaultMaxStepsWithStopWhen
	}

	messages := append([]provider.Message(nil), call.Messages...)

	s := &TextStream{
		ctx:        ctx,
		opts:       opts,
		maxRetries: maxRetries,
		maxSteps:   maxSteps,
		messages:   messages,
	}

	call.Messages = messages
	if opts.PrepareStep != nil {
		if modified, ok := opts.PrepareStep(0, call); ok {
			call = modified
		}
	}

	stream, err := startStream(ctx, opts.Model, call, call.Messages, maxRetries)
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
			var reasoningText string
			var reasoningParts []provider.ReasoningPart
			var toolCalls []provider.ToolCallPart
			type pendingCall struct {
				name string
				args []byte
			}
			argsByID := map[string]*pendingCall{}
			var finish provider.FinishPart
			var gotFinish bool
			abandoned := false

			for p := range stream.Parts() {
				switch part := p.(type) {
				case provider.TextDelta:
					text += part.Text
				case provider.ReasoningDelta:
					reasoningText += part.Text
				case provider.ReasoningEnd:
					reasoningParts = append(reasoningParts, part.Part)
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
			// (e.g. openaicompat reasoning_content) get a single
			// synthesized ReasoningPart from the accumulated text.
			var respContent []provider.ContentPart
			if len(reasoningParts) > 0 {
				for _, rp := range reasoningParts {
					respContent = append(respContent, rp)
				}
			} else if reasoningText != "" {
				respContent = append(respContent, provider.ReasoningPart{Text: reasoningText})
			}
			if text != "" {
				respContent = append(respContent, provider.TextPart{Text: text})
			}
			for _, tc := range toolCalls {
				respContent = append(respContent, tc)
			}
			stepResp := &provider.Response{
				Content:      respContent,
				FinishReason: finish.Reason,
				Usage:        finish.Usage,
			}

			step := Step{
				Text:          text,
				ReasoningText: stepResp.ReasoningText(),
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
			if gotFinish {
				s.lastFinish = finish.Reason
			}

			s.messages = append(s.messages, provider.Message{Role: provider.RoleAssistant, Content: assistantContent(respContent)})

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

			if s.opts.OnStepFinish != nil {
				s.opts.OnStepFinish(step)
			}

			if !hasToolCalls {
				s.current = nil
				return
			}
			if len(s.steps) >= s.maxSteps {
				s.current = nil
				return
			}
			if s.opts.StopWhen != nil && s.opts.StopWhen(s.steps) {
				s.current = nil
				return
			}

			call, err := buildCall(s.opts)
			if err != nil {
				s.err = err
				s.current = nil
				return
			}
			call.Messages = s.messages
			if s.opts.PrepareStep != nil {
				if modified, ok := s.opts.PrepareStep(len(s.steps), call); ok {
					call = modified
				}
			}
			next, err := startStream(s.ctx, s.opts.Model, call, call.Messages, s.maxRetries)
			if err != nil {
				s.err = err
				s.current = nil
				return
			}
			s.current = next
		}
	}
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

// Err returns the error, if any, that ended iteration abnormally: a
// *RetryError if a subsequent step's stream could not start, a
// *NoSuchToolError if an unknown tool was requested, or the underlying
// provider stream's mid-stream error.
func (s *TextStream) Err() error { return s.err }

// Text returns the accumulated text of the final step.
func (s *TextStream) Text() string { return s.lastText }

// ReasoningText returns the accumulated reasoning text of the final step.
func (s *TextStream) ReasoningText() string { return s.lastReasoning }

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
	if s.current == nil {
		return nil
	}
	err := s.current.Close()
	s.current = nil
	return err
}
