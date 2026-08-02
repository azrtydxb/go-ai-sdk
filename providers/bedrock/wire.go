package bedrock

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- Request wire types ----

type wireSystemBlock struct {
	Text string `json:"text"`
}

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

// wireContentBlock is a union of all Converse content block shapes. Exactly
// one of Text/Image/ToolUse/ToolResult is populated when marshaled; omitempty
// keeps the others out of the JSON. Text is a *string (not string) so a
// present-but-empty text block ({"text":""}) can be told apart, on decode,
// from no text block at all — a plain string field would make both cases
// indistinguishable via block.Text != "", causing convertResponse to
// silently drop an empty TextPart from the response.
type wireContentBlock struct {
	Text             *string               `json:"text,omitempty"`
	Image            *wireImage            `json:"image,omitempty"`
	ToolUse          *wireToolUse          `json:"toolUse,omitempty"`
	ToolResult       *wireToolResult       `json:"toolResult,omitempty"`
	ReasoningContent *wireReasoningContent `json:"reasoningContent,omitempty"`
}

func strPtr(s string) *string { return &s }

// wireReasoningContent is the Converse API's reasoningContent block: exactly
// one of ReasoningText (signed, visible thinking) or RedactedContent (an
// opaque base64 blob, when the reasoning trace was redacted) is populated.
type wireReasoningContent struct {
	ReasoningText   *wireReasoningText `json:"reasoningText,omitempty"`
	RedactedContent string             `json:"redactedContent,omitempty"` // base64
}

type wireReasoningText struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type wireImage struct {
	Format string          `json:"format"`
	Source wireImageSource `json:"source"`
}

type wireImageSource struct {
	Bytes string `json:"bytes"` // base64
}

type wireToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type wireToolResult struct {
	ToolUseID string                  `json:"toolUseId"`
	Content   []wireToolResultContent `json:"content"`
	Status    string                  `json:"status,omitempty"`
}

type wireToolResultContent struct {
	JSON json.RawMessage `json:"json,omitempty"`
	Text string          `json:"text,omitempty"`
}

type wireToolConfig struct {
	Tools      []wireTool `json:"tools,omitempty"`
	ToolChoice any        `json:"toolChoice,omitempty"`
}

type wireTool struct {
	ToolSpec wireToolSpec `json:"toolSpec"`
}

type wireToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema wireInputSchema `json:"inputSchema"`
}

type wireInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type wireToolChoiceAuto struct {
	Auto struct{} `json:"auto"`
}

type wireToolChoiceAny struct {
	Any struct{} `json:"any"`
}

type wireToolChoiceTool struct {
	Tool wireToolChoiceToolName `json:"tool"`
}

type wireToolChoiceToolName struct {
	Name string `json:"name"`
}

type wireInferenceConfig struct {
	MaxTokens     *int     `json:"maxTokens,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

type converseRequest struct {
	System          []wireSystemBlock    `json:"system,omitempty"`
	Messages        []wireMessage        `json:"messages"`
	ToolConfig      *wireToolConfig      `json:"toolConfig,omitempty"`
	InferenceConfig *wireInferenceConfig `json:"inferenceConfig,omitempty"`
}

// ---- Response wire types (non-streaming) ----

type converseResponse struct {
	Output     converseOutput `json:"output"`
	StopReason string         `json:"stopReason"`
	Usage      wireUsage      `json:"usage"`
}

type converseOutput struct {
	Message wireMessage `json:"message"`
}

type wireUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// ---- Error wire type ----

type wireError struct {
	Message string `json:"message"`
}

func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Message != "" {
		return we.Message
	}
	return string(body)
}

// ---- Streaming event payload wire types ----

type eventMessageStart struct {
	Role string `json:"role"`
}

type eventContentBlockStart struct {
	ContentBlockIndex int                         `json:"contentBlockIndex"`
	Start             eventContentBlockStartUnion `json:"start"`
}

type eventContentBlockStartUnion struct {
	ToolUse *eventToolUseStart `json:"toolUse,omitempty"`
}

type eventToolUseStart struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
}

type eventContentBlockDelta struct {
	ContentBlockIndex int             `json:"contentBlockIndex"`
	Delta             eventDeltaUnion `json:"delta"`
}

type eventDeltaUnion struct {
	Text             string                      `json:"text,omitempty"`
	ToolUse          *eventToolUseDelta          `json:"toolUse,omitempty"`
	ReasoningContent *eventReasoningContentDelta `json:"reasoningContent,omitempty"`
}

type eventToolUseDelta struct {
	Input string `json:"input"` // partial JSON string fragment
}

// eventReasoningContentDelta is the Converse streaming reasoningContent
// delta shape: each contentBlockDelta carries at most one of Text (a
// fragment of the visible thinking text), Signature (a fragment of the
// trailing signature, delivered after the text is complete), or
// RedactedContent (a base64 fragment, for a redacted reasoning block).
type eventReasoningContentDelta struct {
	Text            string `json:"text,omitempty"`
	Signature       string `json:"signature,omitempty"`
	RedactedContent string `json:"redactedContent,omitempty"`
}

type eventContentBlockStop struct {
	ContentBlockIndex int `json:"contentBlockIndex"`
}

type eventMessageStop struct {
	StopReason string `json:"stopReason"`
}

type eventMetadata struct {
	Usage wireUsage `json:"usage"`
}

type eventException struct {
	Message string `json:"message"`
}

// applyProviderOptions merges providerOptions["bedrock"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built — e.g. {"bedrock": {"additionalModelRequestFields": {...}}} sets
// the Converse API's additionalModelRequestFields wholesale. Returns
// reqBytes unchanged (no unmarshal/marshal round trip) when there's
// nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["bedrock"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("bedrock: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

// ---- Request building ----

func buildConverseRequest(call provider.Call) (converseRequest, error) {
	var req converseRequest

	for _, m := range call.Messages {
		if m.Role == provider.RoleSystem {
			req.System = append(req.System, wireSystemBlock{Text: textContent(m.Content)})
		}
	}

	messages, err := convertMessages(call.Messages)
	if err != nil {
		return converseRequest{}, err
	}
	req.Messages = messages

	// ToolChoiceNone means: omit toolConfig entirely (Bedrock has no "none"
	// tool choice value).
	omitTools := call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceNone
	if !omitTools && call.ToolChoice != nil && len(call.Tools) == 0 {
		// A toolChoice-only toolConfig (no tools) is rejected by Bedrock;
		// fail fast with a descriptive error instead of sending a request
		// that the API will bounce.
		return converseRequest{}, errors.New("bedrock: tool choice requires at least one tool")
	}
	if !omitTools && (len(call.Tools) > 0 || call.ToolChoice != nil) {
		tc := &wireToolConfig{}
		if len(call.Tools) > 0 {
			tc.Tools = convertTools(call.Tools)
		}
		if call.ToolChoice != nil {
			choice, err := convertToolChoice(*call.ToolChoice)
			if err != nil {
				return converseRequest{}, err
			}
			tc.ToolChoice = choice
		}
		req.ToolConfig = tc
	}

	if call.MaxTokens != nil || call.Temperature != nil || call.TopP != nil || len(call.StopSequences) > 0 {
		req.InferenceConfig = &wireInferenceConfig{
			MaxTokens:     call.MaxTokens,
			Temperature:   call.Temperature,
			TopP:          call.TopP,
			StopSequences: call.StopSequences,
		}
	}

	// ResponseFormat is intentionally ignored: Bedrock's Converse API has no
	// response-format field, and Capabilities().NativeJSON is false so the
	// ai core enforces JSON output via tool-mode instead.

	return req, nil
}

func textContent(parts []provider.ContentPart) string {
	var s string
	for _, part := range parts {
		if tp, ok := part.(provider.TextPart); ok {
			s += tp.Text
		}
	}
	return s
}

func convertMessages(msgs []provider.Message) ([]wireMessage, error) {
	var out []wireMessage
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			// Handled separately as the top-level "system" field.
			continue

		case provider.RoleUser:
			content, err := convertUserContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, wireMessage{Role: "user", Content: content})

		case provider.RoleAssistant:
			content, err := assistantBlocks(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, wireMessage{Role: "assistant", Content: content})

		case provider.RoleTool:
			// Bedrock groups tool results into a single "user" message,
			// one toolResult content block per ToolResultPart.
			var content []wireContentBlock
			for _, part := range m.Content {
				trp, ok := part.(provider.ToolResultPart)
				if !ok {
					return nil, fmt.Errorf("bedrock: unsupported content part %T in tool message", part)
				}
				tr := &wireToolResult{ToolUseID: trp.ToolCallID}
				switch v := trp.Result.(type) {
				case string:
					tr.Content = []wireToolResultContent{{Text: v}}
				default:
					b, err := json.Marshal(trp.Result)
					if err != nil {
						return nil, fmt.Errorf("bedrock: marshal tool result: %w", err)
					}
					tr.Content = []wireToolResultContent{{JSON: b}}
				}
				// Unlike Mistral/OpenAI-style tool messages, Bedrock's
				// toolResult block has a dedicated error slot: status
				// "error" (vs. the default "success").
				if trp.IsError {
					tr.Status = "error"
				}
				content = append(content, wireContentBlock{ToolResult: tr})
			}
			out = append(out, wireMessage{Role: "user", Content: content})

		default:
			return nil, fmt.Errorf("bedrock: unsupported message role %q", m.Role)
		}
	}
	return out, nil
}

// assistantBlocks converts an assistant message's content parts into wire
// blocks. Reasoning parts are emitted first, before any other block type —
// mirroring the Anthropic provider's convention, since Converse likewise
// expects a reasoningContent block to lead an assistant turn when it is
// present.
func assistantBlocks(parts []provider.ContentPart) ([]wireContentBlock, error) {
	var reasoning []wireContentBlock
	var rest []wireContentBlock
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			rest = append(rest, wireContentBlock{Text: strPtr(p.Text)})
		case provider.ToolCallPart:
			args := p.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			rest = append(rest, wireContentBlock{ToolUse: &wireToolUse{
				ToolUseID: p.ID,
				Name:      p.Name,
				Input:     args,
			}})
		case provider.SourcePart:
			// informational, not replayable — skip
		case provider.ReasoningPart:
			switch {
			case p.Redacted:
				reasoning = append(reasoning, wireContentBlock{ReasoningContent: &wireReasoningContent{
					RedactedContent: p.Text,
				}})
			case p.Signature != "":
				reasoning = append(reasoning, wireContentBlock{ReasoningContent: &wireReasoningContent{
					ReasoningText: &wireReasoningText{Text: p.Text, Signature: p.Signature},
				}})
			default:
				// A non-redacted ReasoningPart with no signature cannot be
				// replayed (Converse requires a signature on a replayed
				// reasoningText block, like Anthropic's thinking blocks) —
				// informational, not replayable — skip.
			}
		default:
			return nil, fmt.Errorf("bedrock: unsupported content part %T in assistant message", part)
		}
	}
	return append(reasoning, rest...), nil
}

func convertUserContent(parts []provider.ContentPart) ([]wireContentBlock, error) {
	var out []wireContentBlock
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			out = append(out, wireContentBlock{Text: strPtr(p.Text)})
		case provider.ImagePart:
			if len(p.Data) == 0 {
				return nil, fmt.Errorf("bedrock: image parts require inline Data (URL images are not supported)")
			}
			format := imageFormat(p.MediaType)
			out = append(out, wireContentBlock{Image: &wireImage{
				Format: format,
				Source: wireImageSource{Bytes: base64.StdEncoding.EncodeToString(p.Data)},
			}})
		default:
			return nil, fmt.Errorf("bedrock: unsupported content part %T in user message", part)
		}
	}
	return out, nil
}

func imageFormat(mediaType string) string {
	switch mediaType {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func convertTools(tools []provider.ToolDef) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{ToolSpec: wireToolSpec{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: wireInputSchema{JSON: t.Schema},
		}})
	}
	return out
}

func convertToolChoice(tc provider.ToolChoice) (any, error) {
	switch tc.Mode {
	case provider.ToolChoiceAuto:
		return wireToolChoiceAuto{}, nil
	case provider.ToolChoiceRequired:
		return wireToolChoiceAny{}, nil
	case provider.ToolChoiceTool:
		return wireToolChoiceTool{Tool: wireToolChoiceToolName{Name: tc.ToolName}}, nil
	case provider.ToolChoiceNone:
		// Callers omit toolConfig entirely for ToolChoiceNone; reaching
		// here is a programming error in buildConverseRequest.
		return nil, fmt.Errorf("bedrock: ToolChoiceNone must omit toolConfig")
	default:
		return nil, fmt.Errorf("bedrock: unsupported ToolChoice.Mode %q", tc.Mode)
	}
}

// ---- Response conversion ----

func mapStopReason(reason string) provider.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return provider.FinishStop
	case "max_tokens":
		return provider.FinishLength
	case "tool_use":
		return provider.FinishToolCalls
	case "content_filtered":
		return provider.FinishContentFilter
	default:
		return provider.FinishOther
	}
}

func convertResponse(wr converseResponse, raw []byte) *provider.Response {
	resp := &provider.Response{
		Raw:          json.RawMessage(raw),
		FinishReason: mapStopReason(wr.StopReason),
		Usage: provider.Usage{
			InputTokens:  wr.Usage.InputTokens,
			OutputTokens: wr.Usage.OutputTokens,
			TotalTokens:  wr.Usage.TotalTokens,
		},
	}

	for _, block := range wr.Output.Message.Content {
		switch {
		case block.Text != nil:
			resp.Content = append(resp.Content, provider.TextPart{Text: *block.Text})
		case block.ToolUse != nil:
			args := block.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			resp.Content = append(resp.Content, provider.ToolCallPart{
				ID:   block.ToolUse.ToolUseID,
				Name: block.ToolUse.Name,
				Args: args,
			})
		case block.ReasoningContent != nil:
			resp.Content = append(resp.Content, reasoningPartFromWire(block.ReasoningContent))
		}
	}

	return resp
}

// reasoningPartFromWire converts a Converse reasoningContent block into a
// provider.ReasoningPart: a populated ReasoningText carries the signed,
// visible thinking text/signature; a populated RedactedContent (base64) is
// surfaced as a Redacted part whose Text holds the base64 payload verbatim,
// mirroring how the Anthropic provider surfaces redacted_thinking.
func reasoningPartFromWire(rc *wireReasoningContent) provider.ReasoningPart {
	if rc.ReasoningText != nil {
		return provider.ReasoningPart{Text: rc.ReasoningText.Text, Signature: rc.ReasoningText.Signature}
	}
	return provider.ReasoningPart{Redacted: true, Text: rc.RedactedContent}
}
