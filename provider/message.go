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

// FilePart is a file/attachment content part, valid only in user messages
// (assistant-message FilePart is rejected by every provider).
//
// Support matrix:
//   - anthropic: application/pdf only, sent as a "document" content block
//     (Filename, if set, becomes the block's title).
//   - google, vertex (geminicompat): any MediaType, sent inline via
//     inlineData (Gemini accepts PDFs, audio, and video inline).
//   - openai and OpenAI-compatible providers (openaicompat): application/pdf
//     only, sent as a "file" content part with a data: URL. Only OpenAI
//     itself is known to accept this; other OpenAI-compatible servers may
//     reject it — passthrough is correct behavior.
//   - bedrock: a fixed set of document formats recognized from MediaType
//     (application/pdf, text/csv, text/html, text/plain, text/markdown,
//     application/msword, the Office Open XML Word/Excel types), sent as a
//     Converse "document" content block; any other MediaType is rejected.
//   - all other providers (cohere, mistral, ...): unsupported; return a
//     descriptive error.
//
// Where a provider is documented as matching a specific MediaType (e.g.
// anthropic and openaicompat's application/pdf), the match is
// case-insensitive and ignores any MIME parameters (via
// mime.ParseMediaType) — "Application/PDF" and "application/pdf; name=x"
// both match "application/pdf".
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

// SourcePart is a citation/grounding source content part: a URL (and
// optional title) the model consulted or cited while producing its
// response. Currently only geminicompat populates this, from Google's
// groundingMetadata.groundingChunks (see internal/geminicompat/wire.go).
// Anthropic's citations are documented as future work, not covered in this
// wave.
type SourcePart struct {
	ID    string
	URL   string
	Title string
}

func (SourcePart) isContentPart() {}

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
