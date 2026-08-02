package openaicompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- Request wire types ----

type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []wireToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *wireImageURL `json:"image_url,omitempty"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

type wireToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireToolChoiceObj struct {
	Type     string             `json:"type"`
	Function wireToolChoiceFunc `json:"function"`
}

type wireToolChoiceFunc struct {
	Name string `json:"name"`
}

type wireJSONSchemaFormat struct {
	Type       string          `json:"type"`
	JSONSchema *wireJSONSchema `json:"json_schema,omitempty"`
}

type wireJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict bool            `json:"strict"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// defaultMaxTokensParam is the wire field name used to send MaxTokens when
// Config.MaxTokensParam is unset — OpenAI's current field name.
const defaultMaxTokensParam = "max_completion_tokens"

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	Tools          []wireTool    `json:"tools,omitempty"`
	ToolChoice     any           `json:"tool_choice,omitempty"`
	ResponseFormat any           `json:"response_format,omitempty"`
	// MaxTokens is marshaled by MarshalJSON below under maxTokensParam (or
	// defaultMaxTokensParam if unset), not under a static json tag — the
	// wire field name varies per provider (see Config.MaxTokensParam).
	MaxTokens      *int `json:"-"`
	maxTokensParam string
	Temperature    *float64       `json:"temperature,omitempty"`
	TopP           *float64       `json:"top_p,omitempty"`
	Stop           []string       `json:"stop,omitempty"`
	Stream         bool           `json:"stream,omitempty"`
	StreamOptions  *streamOptions `json:"stream_options,omitempty"`
}

// MarshalJSON marshals chatRequest normally, then adds the max-tokens value
// (if any) under the provider-specific field name in r.maxTokensParam
// (falling back to defaultMaxTokensParam when empty).
func (r chatRequest) MarshalJSON() ([]byte, error) {
	type alias chatRequest
	b, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if r.MaxTokens == nil {
		return b, nil
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	paramName := r.maxTokensParam
	if paramName == "" {
		paramName = defaultMaxTokensParam
	}
	mt, err := json.Marshal(*r.MaxTokens)
	if err != nil {
		return nil, err
	}
	m[paramName] = mt
	return json.Marshal(m)
}

// ---- Response wire types (non-streaming) ----

type chatResponse struct {
	Choices []chatResponseChoice `json:"choices"`
	Usage   wireUsage            `json:"usage"`
}

type chatResponseChoice struct {
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatResponseMessage struct {
	Content   *string        `json:"content"`
	ToolCalls []wireToolCall `json:"tool_calls"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ---- Streaming wire types ----

type chatStreamChunk struct {
	Choices []chatStreamChoice `json:"choices"`
	Usage   *wireUsage         `json:"usage"`
}

type chatStreamChoice struct {
	Delta        chatStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type chatStreamDelta struct {
	Content   string              `json:"content"`
	ToolCalls []wireToolCallDelta `json:"tool_calls"`
}

type wireToolCallDelta struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

// ---- Error wire type ----

type wireError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// errorMessage tries to parse OpenAI's {"error":{"message":...}} shape.
// Falls back to the raw body if parsing fails or message is empty.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Error.Message != "" {
		return we.Error.Message
	}
	return string(body)
}

// ---- Request building ----

func buildChatRequest(cfg Config, modelID string, call provider.Call, stream bool) (chatRequest, error) {
	messages, err := convertMessages(call.Messages)
	if err != nil {
		return chatRequest{}, err
	}

	req := chatRequest{
		Model:          modelID,
		Messages:       messages,
		Temperature:    call.Temperature,
		TopP:           call.TopP,
		Stop:           call.StopSequences,
		MaxTokens:      call.MaxTokens,
		maxTokensParam: cfg.MaxTokensParam,
	}

	if len(call.Tools) > 0 {
		req.Tools = convertTools(call.Tools)
	}

	if call.ToolChoice != nil {
		req.ToolChoice = convertToolChoice(*call.ToolChoice)
	}

	if call.ResponseFormat != nil {
		rf, err := convertResponseFormat(*call.ResponseFormat, cfg.JSONObjectOnly)
		if err != nil {
			return chatRequest{}, err
		}
		req.ResponseFormat = rf
	}

	if stream {
		req.Stream = true
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	return req, nil
}

func convertMessages(msgs []provider.Message) ([]wireMessage, error) {
	var out []wireMessage
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			text := textContent(m.Content)
			b, _ := json.Marshal(text)
			out = append(out, wireMessage{Role: "system", Content: b})

		case provider.RoleUser:
			content, err := userContent(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, wireMessage{Role: "user", Content: content})

		case provider.RoleAssistant:
			wm := wireMessage{Role: "assistant"}
			var text string
			var haveText bool
			for _, part := range m.Content {
				switch p := part.(type) {
				case provider.TextPart:
					text += p.Text
					haveText = true
				case provider.ToolCallPart:
					args := string(p.Args)
					if args == "" {
						args = "{}"
					}
					wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
						ID:   p.ID,
						Type: "function",
						Function: wireToolCallFunc{
							Name:      p.Name,
							Arguments: args,
						},
					})
				}
			}
			if haveText {
				b, _ := json.Marshal(text)
				wm.Content = b
			}
			out = append(out, wm)

		case provider.RoleTool:
			for _, part := range m.Content {
				trp, ok := part.(provider.ToolResultPart)
				if !ok {
					return nil, fmt.Errorf("openaicompat: unsupported content part %T in tool message", part)
				}
				resultJSON, err := json.Marshal(trp.Result)
				if err != nil {
					return nil, fmt.Errorf("openaicompat: marshal tool result: %w", err)
				}
				// trp.IsError is intentionally not encoded: OpenAI's "tool"
				// message wire format has no dedicated error slot — the
				// content is always plain text/JSON regardless of whether
				// the tool call succeeded or failed.
				content, _ := json.Marshal(string(resultJSON))
				out = append(out, wireMessage{
					Role:       "tool",
					ToolCallID: trp.ToolCallID,
					Content:    content,
				})
			}

		default:
			return nil, fmt.Errorf("openaicompat: unsupported message role %q", m.Role)
		}
	}
	return out, nil
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

// userContent builds the content field for a user message. Text-only
// messages are sent as a plain JSON string; mixed content (images, or
// multiple parts) is sent as a parts array.
func userContent(parts []provider.ContentPart) (json.RawMessage, error) {
	textOnly := true
	for _, part := range parts {
		if _, ok := part.(provider.TextPart); !ok {
			textOnly = false
			break
		}
	}
	if textOnly {
		b, err := json.Marshal(textContent(parts))
		return b, err
	}

	var wireParts []wireContentPart
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			wireParts = append(wireParts, wireContentPart{Type: "text", Text: p.Text})
		case provider.ImagePart:
			url := p.URL
			if url == "" {
				mediaType := p.MediaType
				if mediaType == "" {
					mediaType = "application/octet-stream"
				}
				url = fmt.Sprintf("data:%s;base64,%s", mediaType, base64.StdEncoding.EncodeToString(p.Data))
			}
			wireParts = append(wireParts, wireContentPart{Type: "image_url", ImageURL: &wireImageURL{URL: url}})
		default:
			return nil, fmt.Errorf("openaicompat: unsupported content part %T in user message", part)
		}
	}
	return json.Marshal(wireParts)
}

func convertTools(tools []provider.ToolDef) []wireTool {
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireTool{
			Type: "function",
			Function: wireToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			},
		})
	}
	return out
}

func convertToolChoice(tc provider.ToolChoice) any {
	switch tc.Mode {
	case provider.ToolChoiceAuto:
		return "auto"
	case provider.ToolChoiceNone:
		return "none"
	case provider.ToolChoiceRequired:
		return "required"
	case provider.ToolChoiceTool:
		return wireToolChoiceObj{
			Type:     "function",
			Function: wireToolChoiceFunc{Name: tc.ToolName},
		}
	default:
		return nil
	}
}

// convertResponseFormat maps provider.ResponseFormat onto the wire's
// response_format field. When jsonObjectOnly is true (e.g. DeepSeek, which
// rejects json_schema), it always sends {"type":"json_object"} and drops
// any Schema — schema conformance for those providers is enforced by the ai
// core's decode step, not by the wire request.
func convertResponseFormat(rf provider.ResponseFormat, jsonObjectOnly bool) (any, error) {
	switch rf.Type {
	case "json":
		if len(rf.Schema) > 0 && !jsonObjectOnly {
			return wireJSONSchemaFormat{
				Type: "json_schema",
				JSONSchema: &wireJSONSchema{
					Name:   rf.Name,
					Schema: rf.Schema,
					Strict: true,
				},
			}, nil
		}
		return wireJSONSchemaFormat{Type: "json_object"}, nil
	case "text", "":
		// "text" (or unset) is OpenAI's default; omit response_format
		// entirely rather than sending an explicit value for it.
		return nil, nil
	default:
		return nil, fmt.Errorf("openaicompat: unsupported ResponseFormat.Type %q", rf.Type)
	}
}

// ---- Response conversion ----

func mapFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "stop":
		return provider.FinishStop
	case "length":
		return provider.FinishLength
	case "tool_calls":
		return provider.FinishToolCalls
	case "content_filter":
		return provider.FinishContentFilter
	default:
		return provider.FinishOther
	}
}

func convertResponse(wr chatResponse, raw []byte) *provider.Response {
	resp := &provider.Response{
		Raw: json.RawMessage(raw),
		Usage: provider.Usage{
			InputTokens:  wr.Usage.PromptTokens,
			OutputTokens: wr.Usage.CompletionTokens,
			TotalTokens:  wr.Usage.TotalTokens,
		},
	}

	if len(wr.Choices) == 0 {
		return resp
	}
	choice := wr.Choices[0]
	resp.FinishReason = mapFinishReason(choice.FinishReason)

	if choice.Message.Content != nil && *choice.Message.Content != "" {
		resp.Content = append(resp.Content, provider.TextPart{Text: *choice.Message.Content})
	}
	for _, tc := range choice.Message.ToolCalls {
		resp.Content = append(resp.Content, provider.ToolCallPart{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: json.RawMessage(tc.Function.Arguments),
		})
	}
	return resp
}

// ---- Embeddings ----

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Usage wireUsage       `json:"usage"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}
