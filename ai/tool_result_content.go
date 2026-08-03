package ai

import "github.com/azrtydxb/go-ai-sdk/provider"

// ToolResultContent is a multi-modal tool result: a Tool's Execute method
// may return a ToolResultContent (or *ToolResultContent) instead of a plain
// value when it wants to attach one or more images alongside (or instead
// of) text — e.g. a screenshot tool, an image-generation tool, or a chart
// renderer.
//
// Provider support for the Images half is uneven, since not every wire
// format has an image slot in a tool result:
//
//   - anthropic serializes it natively: the tool_result content block's
//     "content" becomes an array — one {"type":"text"} block (only when
//     Text is non-empty) followed by one {"type":"image","source":{...}}
//     block per entry in Images.
//   - bedrock (Converse) likewise serializes it natively: the toolResult
//     block's "content" array gets a {"text":...} entry (only when Text is
//     non-empty) followed by one {"image":{...}} entry per entry in Images.
//   - openaicompat, geminicompat, cohere, and mistral have no image slot in
//     their tool-result wire formats; these providers project
//     ToolResultContent down to its Text field only — Images is silently
//     dropped for them. Prefer text-describable results (or a separate,
//     provider-agnostic mechanism such as attaching an image to a
//     subsequent user message) if a script must run identically across
//     these providers and one of the image-capable ones.
//
// A Tool that never needs images can simply return a plain string (or any
// other JSON-marshalable value) from Execute, as before — ToolResultContent
// is opt-in, only for tools that want to attach images.
type ToolResultContent struct {
	Text   string
	Images []provider.GeneratedImage
}
