package provider

import "iter"

type StreamPart interface{ isStreamPart() }

type TextDelta struct{ Text string }

func (TextDelta) isStreamPart() {}

type ToolCallDelta struct {
	ID        string
	Name      string
	ArgsDelta string
}

func (ToolCallDelta) isStreamPart() {}

type ToolCallEnd struct{ Call ToolCallPart } // complete, args fully assembled

func (ToolCallEnd) isStreamPart() {}

type FinishPart struct {
	Reason FinishReason
	Usage  Usage
}

func (FinishPart) isStreamPart() {}

type StreamResponse interface {
	Parts() iter.Seq[StreamPart] // single-use; stops early on error or ctx cancel
	Err() error                  // non-nil after Parts() ends abnormally
	Close() error                // release underlying connection; safe to call twice
}
