package mistral

// Test-side mirrors of Mistral's wire shapes. The production package is an
// openaicompat preset; these decode-only structs let the tests keep
// asserting the exact request/response wire format Mistral expects
// (max_tokens, random_seed, tool_choice "any", ...).

import "encoding/json"

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

type wireTool struct {
	Type     string       `json:"type"`
	Function wireToolFunc `json:"function"`
}

type wireToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
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

type wireToolChoiceObj struct {
	Type     string             `json:"type"`
	Function wireToolChoiceFunc `json:"function"`
}

type wireToolChoiceFunc struct {
	Name string `json:"name"`
}

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
