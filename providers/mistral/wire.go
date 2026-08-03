package mistral

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
	Name       string          `json:"name,omitempty"`
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

type wireResponseFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model            string        `json:"model"`
	Messages         []wireMessage `json:"messages"`
	Tools            []wireTool    `json:"tools,omitempty"`
	ToolChoice       any           `json:"tool_choice,omitempty"`
	ResponseFormat   any           `json:"response_format,omitempty"`
	MaxTokens        *int          `json:"max_tokens,omitempty"`
	Temperature      *float64      `json:"temperature,omitempty"`
	TopP             *float64      `json:"top_p,omitempty"`
	PresencePenalty  *float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
	RandomSeed       *int64        `json:"random_seed,omitempty"` // Mistral's name for Seed
	Stop             []string      `json:"stop,omitempty"`
	Stream           bool          `json:"stream,omitempty"`
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

// wireError mirrors the two shapes Mistral's API is known to use for error
// bodies: a top-level {"message":...} and OpenAI-style {"error":{"message":...}}.
type wireError struct {
	Message string `json:"message"`
	Error   struct {
		Message string `json:"message"`
	} `json:"error"`
}

// errorMessage tries to parse Mistral's error body shapes. Falls back to
// the raw body if parsing fails or no message field is present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil {
		if we.Message != "" {
			return we.Message
		}
		if we.Error.Message != "" {
			return we.Error.Message
		}
	}
	return string(body)
}

// applyProviderOptions merges providerOptions["mistral"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip) when
// there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["mistral"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("mistral: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

// ---- Request building ----

func buildChatRequest(modelID string, call provider.Call, stream bool) (chatRequest, error) {
	messages, err := convertMessages(call.Messages)
	if err != nil {
		return chatRequest{}, err
	}

	req := chatRequest{
		Model:            modelID,
		Messages:         messages,
		MaxTokens:        call.MaxTokens,
		Temperature:      call.Temperature,
		TopP:             call.TopP,
		PresencePenalty:  call.PresencePenalty,
		FrequencyPenalty: call.FrequencyPenalty,
		RandomSeed:       call.Seed,
		Stop:             call.StopSequences,
	}
	// call.TopK is intentionally ignored: Mistral's chat completions API has
	// no top_k parameter.

	// ToolChoiceNone means: omit tools entirely, not merely tool_choice —
	// Mistral rejects tool_choice without a non-empty tools array.
	omitTools := call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceNone

	if !omitTools {
		if len(call.Tools) > 0 {
			req.Tools = convertTools(call.Tools)
		}
		if call.ToolChoice != nil {
			req.ToolChoice = convertToolChoice(*call.ToolChoice)
		}
	}

	if call.ResponseFormat != nil {
		rf, err := convertResponseFormat(*call.ResponseFormat)
		if err != nil {
			return chatRequest{}, err
		}
		req.ResponseFormat = rf
	}

	if stream {
		req.Stream = true
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
				case provider.SourcePart:
					// informational, not replayable — skip
				case provider.ReasoningPart:
					// informational, not replayable — skip (mistral has no
					// wire representation for reasoning/thinking blocks)
				default:
					return nil, fmt.Errorf("mistral: unsupported content part %T in assistant message", part)
				}
			}
			if haveText {
				b, _ := json.Marshal(text)
				wm.Content = b
			}
			out = append(out, wm)

		case provider.RoleTool:
			// One message per ToolResultPart, per Mistral's wire format
			// (unlike Anthropic, which packs multiple tool results into
			// one user message's content blocks).
			for _, part := range m.Content {
				trp, ok := part.(provider.ToolResultPart)
				if !ok {
					return nil, fmt.Errorf("mistral: unsupported content part %T in tool message", part)
				}
				resultJSON, err := json.Marshal(trp.Result)
				if err != nil {
					return nil, fmt.Errorf("mistral: marshal tool result: %w", err)
				}
				// trp.IsError is intentionally not encoded: Mistral's "tool"
				// message wire format has no dedicated error slot — the
				// content is always plain text/JSON regardless of whether
				// the tool call succeeded or failed (openai convention).
				content, _ := json.Marshal(string(resultJSON))
				out = append(out, wireMessage{
					Role:       "tool",
					ToolCallID: trp.ToolCallID,
					Name:       trp.Name,
					Content:    content,
				})
			}

		default:
			return nil, fmt.Errorf("mistral: unsupported message role %q", m.Role)
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
			return nil, fmt.Errorf("mistral: unsupported content part %T in user message", part)
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
		// Mistral's word for "required" is "any".
		return "any"
	case provider.ToolChoiceTool:
		return wireToolChoiceObj{
			Type:     "function",
			Function: wireToolChoiceFunc{Name: tc.ToolName},
		}
	default:
		return nil
	}
}

// convertResponseFormat maps provider.ResponseFormat onto Mistral's
// response_format field. Mistral has no json_schema mode: for
// ResponseFormat.Type == "json" it sends {"type":"json_object"} and ignores
// any Schema — schema conformance for Mistral models is enforced by the ai
// core's decode step, not by the wire request.
func convertResponseFormat(rf provider.ResponseFormat) (any, error) {
	switch rf.Type {
	case "json":
		return wireResponseFormat{Type: "json_object"}, nil
	case "text", "":
		// "text" (or unset) is the default; omit response_format entirely
		// rather than sending an explicit value for it.
		return nil, nil
	default:
		return nil, fmt.Errorf("mistral: unsupported ResponseFormat.Type %q", rf.Type)
	}
}

// ---- Response conversion ----

func mapFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "stop":
		return provider.FinishStop
	case "length", "model_length":
		return provider.FinishLength
	case "tool_calls":
		return provider.FinishToolCalls
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
