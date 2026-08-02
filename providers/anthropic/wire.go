package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- Request wire types ----

type wireMessage struct {
	Role    string             `json:"role"`
	Content []wireContentBlock `json:"content"`
}

type wireImageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// wireContentBlock is a union of every content block shape the Messages API
// accepts, both in requests and responses. Only the fields relevant to
// block.Type are populated at any one time.
type wireContentBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *wireImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   *bool  `json:"is_error,omitempty"`

	// thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// redacted_thinking
	Data string `json:"data,omitempty"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type messagesRequest struct {
	Model         string        `json:"model"`
	System        string        `json:"system,omitempty"`
	Messages      []wireMessage `json:"messages"`
	MaxTokens     int           `json:"max_tokens"`
	Tools         []wireTool    `json:"tools,omitempty"`
	ToolChoice    any           `json:"tool_choice,omitempty"`
	StopSequences []string      `json:"stop_sequences,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	TopP          *float64      `json:"top_p,omitempty"`
	Stream        bool          `json:"stream,omitempty"`
}

// ---- Response wire types (non-streaming) ----

type messageResponse struct {
	Content    []wireContentBlock `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      wireUsage          `json:"usage"`
}

type wireUsage struct {
	InputTokens          int `json:"input_tokens"`
	OutputTokens         int `json:"output_tokens"`
	CacheReadInputTokens int `json:"cache_read_input_tokens,omitempty"`
	// CacheCreationInputTokens is the number of input tokens written to the
	// prompt cache on this request (as opposed to read from it — see
	// CacheReadInputTokens). Surfaced via
	// Response.ProviderMetadata["anthropic"]["cache_creation_input_tokens"]
	// when non-zero.
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// ---- Streaming wire types ----

type streamContentBlock struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`

	// thinking (rarely non-empty at start; thinking text normally arrives
	// via thinking_delta instead)
	Thinking string `json:"thinking,omitempty"`

	// redacted_thinking: the full opaque payload, delivered whole at
	// content_block_start (redacted_thinking blocks have no deltas)
	Data string `json:"data,omitempty"`
}

type contentBlockStartEvent struct {
	Index        int                `json:"index"`
	ContentBlock streamContentBlock `json:"content_block"`
}

type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`

	// thinking_delta
	Thinking string `json:"thinking,omitempty"`

	// signature_delta
	Signature string `json:"signature,omitempty"`
}

type contentBlockDeltaEvent struct {
	Index int         `json:"index"`
	Delta streamDelta `json:"delta"`
}

type contentBlockStopEvent struct {
	Index int `json:"index"`
}

type messageDeltaEvent struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage *messageDeltaUsage `json:"usage,omitempty"`
}

type messageDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type messageStartEvent struct {
	Message struct {
		Usage *messageStartUsage `json:"usage,omitempty"`
	} `json:"message"`
}

type messageStartUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// ---- Error wire type ----

type wireError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// errorMessage tries to parse Anthropic's {"error":{"message":...}} shape.
// Falls back to the raw body if parsing fails or message is empty.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Error.Message != "" {
		return we.Error.Message
	}
	return string(body)
}

// applyProviderOptions merges providerOptions["anthropic"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip) when
// there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["anthropic"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("anthropic: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

// ---- Request building ----

func buildMessagesRequest(modelID string, call provider.Call, stream bool) (messagesRequest, error) {
	system, messages, err := convertMessages(call.Messages)
	if err != nil {
		return messagesRequest{}, err
	}

	maxTokens := defaultMaxTokens
	if call.MaxTokens != nil {
		maxTokens = *call.MaxTokens
	}

	req := messagesRequest{
		Model:         modelID,
		System:        system,
		Messages:      messages,
		MaxTokens:     maxTokens,
		StopSequences: call.StopSequences,
		Temperature:   call.Temperature,
		TopP:          call.TopP,
	}

	// ToolChoiceNone means: omit tools entirely, not merely tool_choice.
	omitTools := call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceNone

	if !omitTools {
		if len(call.Tools) > 0 {
			req.Tools = convertTools(call.Tools)
		}
		if call.ToolChoice != nil {
			req.ToolChoice = convertToolChoice(*call.ToolChoice)
		}
	}

	if stream {
		req.Stream = true
	}

	return req, nil
}

func convertMessages(msgs []provider.Message) (system string, out []wireMessage, err error) {
	var systemParts []string
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			systemParts = append(systemParts, textContent(m.Content))

		case provider.RoleUser:
			blocks, uerr := userBlocks(m.Content)
			if uerr != nil {
				return "", nil, uerr
			}
			out = append(out, wireMessage{Role: "user", Content: blocks})

		case provider.RoleAssistant:
			blocks, aerr := assistantBlocks(m.Content)
			if aerr != nil {
				return "", nil, aerr
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})

		case provider.RoleTool:
			blocks, terr := toolResultBlocks(m.Content)
			if terr != nil {
				return "", nil, terr
			}
			out = append(out, wireMessage{Role: "user", Content: blocks})

		default:
			return "", nil, fmt.Errorf("anthropic: unsupported message role %q", m.Role)
		}
	}
	return strings.Join(systemParts, "\n\n"), out, nil
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

func userBlocks(parts []provider.ContentPart) ([]wireContentBlock, error) {
	var out []wireContentBlock
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			out = append(out, wireContentBlock{Type: "text", Text: p.Text})
		case provider.ImagePart:
			block := wireContentBlock{Type: "image"}
			if p.URL != "" {
				block.Source = &wireImageSource{Type: "url", URL: p.URL}
			} else {
				mediaType := p.MediaType
				if mediaType == "" {
					mediaType = "application/octet-stream"
				}
				block.Source = &wireImageSource{
					Type:      "base64",
					MediaType: mediaType,
					Data:      base64.StdEncoding.EncodeToString(p.Data),
				}
			}
			out = append(out, block)
		case provider.FilePart:
			if p.MediaType != "application/pdf" {
				return nil, fmt.Errorf("anthropic: unsupported content part %T with media type %q in user message (only application/pdf is supported)", part, p.MediaType)
			}
			out = append(out, wireContentBlock{
				Type: "document",
				Source: &wireImageSource{
					Type:      "base64",
					MediaType: "application/pdf",
					Data:      base64.StdEncoding.EncodeToString(p.Data),
				},
			})
		default:
			return nil, fmt.Errorf("anthropic: unsupported content part %T in user message", part)
		}
	}
	return out, nil
}

// assistantBlocks converts an assistant message's content parts into wire
// blocks. Reasoning parts are emitted first, before any other block type —
// the Messages API requires thinking/redacted_thinking blocks to lead an
// assistant turn when thinking is enabled.
func assistantBlocks(parts []provider.ContentPart) ([]wireContentBlock, error) {
	var reasoning []wireContentBlock
	var rest []wireContentBlock
	for _, part := range parts {
		switch p := part.(type) {
		case provider.ReasoningPart:
			switch {
			case p.Redacted:
				reasoning = append(reasoning, wireContentBlock{Type: "redacted_thinking", Data: p.Text})
			case p.Signature != "":
				reasoning = append(reasoning, wireContentBlock{Type: "thinking", Thinking: p.Text, Signature: p.Signature})
			default:
				// A non-redacted ReasoningPart with no signature cannot form
				// a valid thinking block (the Messages API requires a
				// signature on replayed thinking blocks) — informational,
				// not replayable — skip, same convention as SourcePart.
			}
		case provider.TextPart:
			rest = append(rest, wireContentBlock{Type: "text", Text: p.Text})
		case provider.ToolCallPart:
			input, err := parsedArgs(p.Args)
			if err != nil {
				return nil, fmt.Errorf("anthropic: parse tool call args: %w", err)
			}
			rest = append(rest, wireContentBlock{
				Type:  "tool_use",
				ID:    p.ID,
				Name:  p.Name,
				Input: input,
			})
		case provider.SourcePart:
			// informational, not replayable — skip
		default:
			return nil, fmt.Errorf("anthropic: unsupported content part %T in assistant message", part)
		}
	}
	return append(reasoning, rest...), nil
}

// parsedArgs unmarshals raw tool-call args JSON into a generic value and
// re-marshals it, so the wire "input" field is always a parsed JSON object
// (never a raw/escaped string), per the Messages API tool_use shape. An
// empty/absent Args defaults to an empty object.
func parsedArgs(raw json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage("{}"), nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func toolResultBlocks(parts []provider.ContentPart) ([]wireContentBlock, error) {
	var out []wireContentBlock
	for _, part := range parts {
		trp, ok := part.(provider.ToolResultPart)
		if !ok {
			return nil, fmt.Errorf("anthropic: unsupported content part %T in tool message", part)
		}
		resultJSON, err := json.Marshal(trp.Result)
		if err != nil {
			return nil, fmt.Errorf("anthropic: marshal tool result: %w", err)
		}
		isErr := trp.IsError
		out = append(out, wireContentBlock{
			Type:      "tool_result",
			ToolUseID: trp.ToolCallID,
			Content:   string(resultJSON),
			IsError:   &isErr,
		})
	}
	return out, nil
}

func convertTools(tools []provider.ToolDef) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Schema,
		})
	}
	return out
}

func convertToolChoice(tc provider.ToolChoice) any {
	switch tc.Mode {
	case provider.ToolChoiceAuto:
		return map[string]string{"type": "auto"}
	case provider.ToolChoiceRequired:
		return map[string]string{"type": "any"}
	case provider.ToolChoiceTool:
		return map[string]string{"type": "tool", "name": tc.ToolName}
	default:
		return nil
	}
}

// ---- Response conversion ----

func mapStopReason(reason string) provider.FinishReason {
	switch reason {
	case "end_turn":
		return provider.FinishStop
	case "max_tokens":
		return provider.FinishLength
	case "tool_use":
		return provider.FinishToolCalls
	case "stop_sequence":
		return provider.FinishStop
	default:
		return provider.FinishOther
	}
}

func convertResponse(wr messageResponse, raw []byte) *provider.Response {
	resp := &provider.Response{
		Raw:          json.RawMessage(raw),
		FinishReason: mapStopReason(wr.StopReason),
		Usage: provider.Usage{
			InputTokens:       wr.Usage.InputTokens,
			OutputTokens:      wr.Usage.OutputTokens,
			TotalTokens:       wr.Usage.InputTokens + wr.Usage.OutputTokens,
			CachedInputTokens: wr.Usage.CacheReadInputTokens,
		},
	}

	if wr.Usage.CacheCreationInputTokens != 0 {
		resp.ProviderMetadata = map[string]any{
			"anthropic": map[string]any{
				"cache_creation_input_tokens": wr.Usage.CacheCreationInputTokens,
			},
		}
	}

	for _, block := range wr.Content {
		switch block.Type {
		case "text":
			resp.Content = append(resp.Content, provider.TextPart{Text: block.Text})
		case "tool_use":
			args := block.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			resp.Content = append(resp.Content, provider.ToolCallPart{
				ID:   block.ID,
				Name: block.Name,
				Args: args,
			})
		case "thinking":
			resp.Content = append(resp.Content, provider.ReasoningPart{Text: block.Thinking, Signature: block.Signature})
		case "redacted_thinking":
			resp.Content = append(resp.Content, provider.ReasoningPart{Redacted: true, Text: block.Data})
		}
	}
	return resp
}
