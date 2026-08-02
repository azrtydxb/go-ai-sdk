package provider

import (
	"encoding/json"
	"strings"
)

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool-calls"
	FinishContentFilter FinishReason = "content-filter"
	FinishError         FinishReason = "error"
	FinishOther         FinishReason = "other"
)

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type Response struct {
	Content      []ContentPart
	FinishReason FinishReason
	Usage        Usage
	Raw          json.RawMessage // raw provider response body
}

// Text concatenates all TextParts in the response
func (r *Response) Text() string {
	var sb strings.Builder
	for _, part := range r.Content {
		if tp, ok := part.(TextPart); ok {
			sb.WriteString(tp.Text)
		}
	}
	return sb.String()
}

// ToolCalls returns all ToolCallParts in the response
func (r *Response) ToolCalls() []ToolCallPart {
	var calls []ToolCallPart
	for _, part := range r.Content {
		if tcp, ok := part.(ToolCallPart); ok {
			calls = append(calls, tcp)
		}
	}
	return calls
}
