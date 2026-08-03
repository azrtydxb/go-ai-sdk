package cohere

import (
	"encoding/json"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- Request wire types ----

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
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

type wireResponseFormat struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema,omitempty"`
}

type chatRequest struct {
	Model            string        `json:"model"`
	Messages         []wireMessage `json:"messages"`
	Tools            []wireTool    `json:"tools,omitempty"`
	ToolChoice       string        `json:"tool_choice,omitempty"`
	ResponseFormat   any           `json:"response_format,omitempty"`
	MaxTokens        *int          `json:"max_tokens,omitempty"`
	Temperature      *float64      `json:"temperature,omitempty"`
	P                *float64      `json:"p,omitempty"` // Cohere's name for top_p
	K                *int          `json:"k,omitempty"` // Cohere's name for top_k
	PresencePenalty  *float64      `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
	Seed             *int64        `json:"seed,omitempty"`
	StopSequences    []string      `json:"stop_sequences,omitempty"`
	Stream           bool          `json:"stream,omitempty"`
}

// ---- Response wire types (non-streaming) ----

type chatResponse struct {
	Message      *chatResponseMessage `json:"message"`
	FinishReason string               `json:"finish_reason"`
	Usage        chatResponseUsage    `json:"usage"`
}

type chatResponseMessage struct {
	Content   []chatResponseContent `json:"content"`
	ToolCalls []wireToolCall        `json:"tool_calls"`
}

type chatResponseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type chatResponseUsage struct {
	Tokens chatResponseTokens `json:"tokens"`
}

type chatResponseTokens struct {
	InputTokens  float64 `json:"input_tokens"`
	OutputTokens float64 `json:"output_tokens"`
}

// ---- Streaming wire types ----

// streamEvent is the envelope for every Cohere v2 chat SSE "data:" payload.
// Cohere does not vary the SSE "event:" field per type the way some other
// providers do; every payload carries its own "type" discriminator, so
// dispatch happens on this field rather than on sse.Event.Event.
type streamEvent struct {
	Type  string       `json:"type"`
	Index *int         `json:"index,omitempty"`
	Delta *streamDelta `json:"delta,omitempty"`
}

type streamDelta struct {
	Message      *streamMessage `json:"message,omitempty"`
	FinishReason string         `json:"finish_reason,omitempty"`
	Usage        *streamUsage   `json:"usage,omitempty"`
}

type streamMessage struct {
	Content   *streamContent  `json:"content,omitempty"`
	ToolCalls *streamToolCall `json:"tool_calls,omitempty"`
}

type streamContent struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}

type streamToolCall struct {
	ID       string              `json:"id,omitempty"`
	Type     string              `json:"type,omitempty"`
	Function *streamToolCallFunc `json:"function,omitempty"`
}

type streamToolCallFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type streamUsage struct {
	Tokens chatResponseTokens `json:"tokens"`
}

// ---- Error wire type ----

type wireError struct {
	Message string `json:"message"`
}

// errorMessage tries to parse Cohere's {"message":...} error body shape.
// Falls back to the raw body if parsing fails or no message field is
// present.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Message != "" {
		return we.Message
	}
	return string(body)
}

// applyProviderOptions merges providerOptions["cohere"] (when it is a
// non-empty map[string]any) into the already-marshaled JSON object
// reqBytes, entries from the option map winning over whatever the SDK
// built. Returns reqBytes unchanged (no unmarshal/marshal round trip) when
// there's nothing to merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any) ([]byte, error) {
	opts, _ := providerOptions["cohere"].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("cohere: unmarshal request for provider options merge: %w", err)
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
		P:                call.TopP,
		K:                call.TopK,
		PresencePenalty:  call.PresencePenalty,
		FrequencyPenalty: call.FrequencyPenalty,
		Seed:             call.Seed,
		StopSequences:    call.StopSequences,
	}
	// call.Reasoning is intentionally ignored: Cohere's chat API has no
	// reasoning/thinking knob.

	switch {
	case call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceNone:
		// ToolChoiceNone: omit tools entirely. Cohere v2 has no way to
		// force "don't call tools" other than omitting tool definitions.
	case call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceTool:
		// ToolChoiceTool: send ONLY that one tool's definition plus
		// tool_choice:"REQUIRED" so Cohere must call a tool from the
		// (single-entry) list.
		var found bool
		for _, t := range call.Tools {
			if t.Name == call.ToolChoice.ToolName {
				req.Tools = convertTools([]provider.ToolDef{t})
				req.ToolChoice = "REQUIRED"
				found = true
				break
			}
		}
		if !found {
			return chatRequest{}, fmt.Errorf("cohere: tool choice %q not in provided tools", call.ToolChoice.ToolName)
		}
	case call.ToolChoice != nil && call.ToolChoice.Mode == provider.ToolChoiceRequired:
		// ToolChoiceRequired: Cohere v2's native tool_choice:"REQUIRED"
		// forces a tool call from the full tool list.
		if len(call.Tools) > 0 {
			req.Tools = convertTools(call.Tools)
		}
		req.ToolChoice = "REQUIRED"
	default:
		// ToolChoiceAuto (Cohere's default; no field sent): just send the
		// tools list when present and let Cohere decide.
		if len(call.Tools) > 0 {
			req.Tools = convertTools(call.Tools)
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
			text, err := onlyText(m.Content, "system")
			if err != nil {
				return nil, err
			}
			out = append(out, wireMessage{Role: "system", Content: text})

		case provider.RoleUser:
			text, err := onlyText(m.Content, "user")
			if err != nil {
				return nil, err
			}
			out = append(out, wireMessage{Role: "user", Content: text})

		case provider.RoleAssistant:
			wm := wireMessage{Role: "assistant"}
			var text string
			for _, part := range m.Content {
				switch p := part.(type) {
				case provider.TextPart:
					text += p.Text
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
					// informational, not replayable — skip (cohere has no
					// wire representation for reasoning/thinking blocks)
				default:
					return nil, fmt.Errorf("cohere: unsupported content part %T in assistant message", part)
				}
			}
			wm.Content = text
			out = append(out, wm)

		case provider.RoleTool:
			// One message per ToolResultPart, per Cohere v2's wire format.
			for _, part := range m.Content {
				trp, ok := part.(provider.ToolResultPart)
				if !ok {
					return nil, fmt.Errorf("cohere: unsupported content part %T in tool message", part)
				}
				resultJSON, err := json.Marshal(toolResultForWire(trp.Result))
				if err != nil {
					return nil, fmt.Errorf("cohere: marshal tool result: %w", err)
				}
				// trp.IsError is intentionally not encoded: Cohere v2's
				// "tool" message wire format has no dedicated error slot —
				// the content is always plain JSON text regardless of
				// whether the tool call succeeded or failed.
				out = append(out, wireMessage{
					Role:       "tool",
					ToolCallID: trp.ToolCallID,
					Content:    string(resultJSON),
				})
			}

		default:
			return nil, fmt.Errorf("cohere: unsupported message role %q", m.Role)
		}
	}
	return out, nil
}

// toolResultForWire projects an ai.ToolResultContent (or
// *ai.ToolResultContent) tool result down to its Text field, since Cohere
// v2's "tool" message content has no image slot; every other value passes
// through unchanged for the normal json.Marshal path above. Images
// attached via ai.ToolResultContent are silently dropped for this
// provider — see ai.ToolResultContent's doc comment.
func toolResultForWire(result any) any {
	switch v := result.(type) {
	case ai.ToolResultContent:
		return v.Text
	case *ai.ToolResultContent:
		if v == nil {
			return nil
		}
		return v.Text
	default:
		return result
	}
}

// onlyText concatenates TextParts in content and errors on any other part
// kind (e.g. images) — Cohere v2 chat is text-only in this integration.
func onlyText(parts []provider.ContentPart, role string) (string, error) {
	var s string
	for _, part := range parts {
		tp, ok := part.(provider.TextPart)
		if !ok {
			return "", fmt.Errorf("cohere: %s message content part %T unsupported (Cohere v2 chat is text-only in this integration)", role, part)
		}
		s += tp.Text
	}
	return s, nil
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

// convertResponseFormat maps provider.ResponseFormat onto Cohere's
// response_format field. Unlike Mistral, Cohere v2 does support a schema
// alongside the json_object type, so it is included when provided.
func convertResponseFormat(rf provider.ResponseFormat) (any, error) {
	switch rf.Type {
	case "json":
		return wireResponseFormat{Type: "json_object", Schema: rf.Schema}, nil
	case "text", "":
		// "text" (or unset) is the default; omit response_format entirely
		// rather than sending an explicit value for it.
		return nil, nil
	default:
		return nil, fmt.Errorf("cohere: unsupported ResponseFormat.Type %q", rf.Type)
	}
}

// ---- Response conversion ----

func mapFinishReason(reason string) provider.FinishReason {
	switch reason {
	case "COMPLETE":
		return provider.FinishStop
	case "MAX_TOKENS":
		return provider.FinishLength
	case "TOOL_CALL":
		return provider.FinishToolCalls
	case "STOP_SEQUENCE":
		return provider.FinishStop
	default:
		return provider.FinishOther
	}
}

func convertResponse(wr chatResponse, raw []byte) *provider.Response {
	resp := &provider.Response{
		Raw:          json.RawMessage(raw),
		FinishReason: mapFinishReason(wr.FinishReason),
		Usage: provider.Usage{
			InputTokens:  int(wr.Usage.Tokens.InputTokens),
			OutputTokens: int(wr.Usage.Tokens.OutputTokens),
			TotalTokens:  int(wr.Usage.Tokens.InputTokens) + int(wr.Usage.Tokens.OutputTokens),
		},
	}

	if wr.Message == nil {
		return resp
	}

	for _, c := range wr.Message.Content {
		if c.Type == "text" && c.Text != "" {
			resp.Content = append(resp.Content, provider.TextPart{Text: c.Text})
		}
	}
	for _, tc := range wr.Message.ToolCalls {
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
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types"`
}

type embeddingResponse struct {
	Embeddings embeddingsWire `json:"embeddings"`
	Meta       embeddingMeta  `json:"meta"`
}

type embeddingsWire struct {
	Float [][]float64 `json:"float"`
}

type embeddingMeta struct {
	BilledUnits embeddingBilledUnits `json:"billed_units"`
}

type embeddingBilledUnits struct {
	InputTokens float64 `json:"input_tokens"`
}

// ---- Rerank ----

type rerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      *int     `json:"top_n,omitempty"`
}

type rerankResponse struct {
	Results []rerankResultWire `json:"results"`
	Meta    rerankMeta         `json:"meta"`
}

type rerankResultWire struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type rerankMeta struct {
	BilledUnits rerankBilledUnits `json:"billed_units"`
}

type rerankBilledUnits struct {
	SearchUnits float64 `json:"search_units"`
}
