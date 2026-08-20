package provider

import "encoding/json"

// Role is the author of a Message: who the content came from, as every
// provider's wire format distinguishes them.
type Role string

// The four message authors this SDK models. Providers that lack a native
// system role (or a distinct tool role) fold these into their own shape in
// their wire.go; the SDK always speaks in these four.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in a conversation: an author and the ordered content
// parts that turn carried. A single message may mix parts — text alongside
// images, or an assistant's reasoning alongside the tool calls it decided
// on.
type Message struct {
	Role    Role
	Content []ContentPart
}

// ContentPart is one piece of a Message's content. The interface is closed
// — only this package can implement it — so a type switch over the parts
// below is exhaustive, and adding a part is a deliberate, reviewable change
// rather than something a caller can do from outside.
type ContentPart interface{ isContentPart() }

// TextPart is plain text, the part every provider supports in every role.
type TextPart struct{ Text string }

func (TextPart) isContentPart() {}

// ImagePart is an image content part, valid in user messages. Exactly one
// of Data (inline bytes) or URL (externally hosted) should be set; MediaType
// describes Data and is required when Data is used, since providers that
// send inline images need the type on the wire.
type ImagePart struct {
	Data      []byte // inline data; exactly one of Data/URL set
	URL       string
	MediaType string // e.g. "image/png"
}

func (ImagePart) isContentPart() {}

// FilePart is a file/attachment content part, valid only in user messages
// (assistant-message FilePart is rejected by every provider). Exactly one of
// Data, FileID, or URL should be set; a converter rejects a FilePart with
// none set, or with more than one set.
//
// Support matrix (inline Data):
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
//
// FileID references a previously-uploaded provider file (see
// provider.FileStore). URL references an externally-hosted file. Support:
//   - FileID — openaicompat (a "file" content part with
//     {"file":{"file_id":...}}), anthropic (a "document" block with source
//     {"type":"file","file_id":...}).
//   - URL — geminicompat (a fileData part with fileUri; also accepts Gemini
//     Files API URIs), anthropic PDFs (a "document" block with source
//     {"type":"url","url":...}).
//
// Families without support for the set variant reject the message (same
// rule as unsupported Data media types).
type FilePart struct {
	Data      []byte
	MediaType string
	Filename  string

	// FileID and URL are documented on the type's doc comment above.
	FileID string
	URL    string
}

func (FilePart) isContentPart() {}

// ToolCallPart is a model's request to invoke a tool: which tool, with what
// arguments, under an ID the matching ToolResultPart quotes back. Args is
// raw JSON rather than a decoded map because the SDK never needs to look
// inside it — the tool's own schema decides what it means.
type ToolCallPart struct {
	ID   string
	Name string
	Args json.RawMessage
}

func (ToolCallPart) isContentPart() {}

// ToolResultPart is the outcome of a tool invocation, returned to the model
// in a message so it can continue. ToolCallID must match the ToolCallPart
// that asked for it. IsError reports a failed invocation: the result is
// still sent (models recover from tool errors when they can see them),
// flagged so the provider can mark it as such on the wire.
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

// UserText builds a single-part user message from a string — the common
// case, without the ContentPart ceremony.
func UserText(text string) Message {
	return Message{
		Role:    RoleUser,
		Content: []ContentPart{TextPart{text}},
	}
}

// SystemText builds a single-part system message from a string.
func SystemText(text string) Message {
	return Message{
		Role:    RoleSystem,
		Content: []ContentPart{TextPart{text}},
	}
}

// AssistantText builds a single-part assistant message from a string,
// for seeding a conversation with prior model turns.
func AssistantText(text string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: []ContentPart{TextPart{text}},
	}
}
