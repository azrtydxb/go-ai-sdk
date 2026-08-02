package provider

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role    Role
	Content []ContentPart
}

type ContentPart interface{ isContentPart() }

type TextPart struct{ Text string }

func (TextPart) isContentPart() {}

type ImagePart struct {
	Data      []byte // inline data; exactly one of Data/URL set
	URL       string
	MediaType string // e.g. "image/png"
}

func (ImagePart) isContentPart() {}

// FilePart is a file/attachment content part. Note: no built-in provider
// currently supports FilePart; providers return a descriptive error.
type FilePart struct {
	Data      []byte
	MediaType string
	Filename  string
}

func (FilePart) isContentPart() {}

type ToolCallPart struct {
	ID   string
	Name string
	Args json.RawMessage
}

func (ToolCallPart) isContentPart() {}

type ToolResultPart struct {
	ToolCallID string
	Name       string
	Result     any // JSON-marshalable
	IsError    bool
}

func (ToolResultPart) isContentPart() {}

// ReasoningPart is a reasoning/thinking content part. Signature and
// Redacted are Anthropic-specific fields: Signature preserves the
// cryptographic signature Anthropic attaches to a visible thinking block,
// required to round-trip the block back to the API in a later request; a
// redacted_thinking block sets Redacted true and Text holds the opaque
// encrypted data rather than readable reasoning text.
type ReasoningPart struct {
	Text      string
	Redacted  bool
	Signature string
}

func (ReasoningPart) isContentPart() {}

// Helper functions
func UserText(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []ContentPart{TextPart{text}},
	}
}

func SystemText(text string) Message {
	return Message{
		Role:    RoleSystem,
		Content: []ContentPart{TextPart{text}},
	}
}

func AssistantText(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: []ContentPart{TextPart{text}},
	}
}
