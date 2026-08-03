package geminicompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// ---- Request wire types ----

type wireContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []wirePart `json:"parts"`
}

type wirePart struct {
	Text             string                `json:"text,omitempty"`
	InlineData       *wireInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *wireFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *wireFunctionResponse `json:"functionResponse,omitempty"`
}

type wireInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type wireFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type wireFunctionResponse struct {
	Name     string                       `json:"name"`
	Response wireFunctionResponseContents `json:"response"`
}

type wireFunctionResponseContents struct {
	Output any `json:"output"`
}

type wireFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireToolDecl struct {
	FunctionDeclarations []wireFunctionDeclaration `json:"functionDeclarations"`
}

type wireFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type wireToolConfig struct {
	FunctionCallingConfig wireFunctionCallingConfig `json:"functionCallingConfig"`
}

type wireGenerationConfig struct {
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
	MaxOutputTokens  *int            `json:"maxOutputTokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	TopK             *int            `json:"topK,omitempty"`
	PresencePenalty  *float64        `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequencyPenalty,omitempty"`
	Seed             *int64          `json:"seed,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
}

type generateContentRequest struct {
	SystemInstruction *wireContent          `json:"systemInstruction,omitempty"`
	Contents          []wireContent         `json:"contents"`
	Tools             []wireToolDecl        `json:"tools,omitempty"`
	ToolConfig        *wireToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *wireGenerationConfig `json:"generationConfig,omitempty"`
}

// ---- Response wire types (non-streaming and streaming share this shape)
// ----

type generateContentResponse struct {
	Candidates    []wireCandidate    `json:"candidates"`
	UsageMetadata *wireUsageMetadata `json:"usageMetadata,omitempty"`
}

type wireCandidate struct {
	Content           wireContent        `json:"content"`
	FinishReason      string             `json:"finishReason,omitempty"`
	GroundingMetadata *wireGroundingMeta `json:"groundingMetadata,omitempty"`
}

// wireGroundingMeta is Google's Grounding with Google Search metadata:
// candidates[0].groundingMetadata.groundingChunks[].web{uri,title}. Each
// chunk becomes a provider.SourcePart (see groundingSources).
type wireGroundingMeta struct {
	GroundingChunks []wireGroundingChunk `json:"groundingChunks,omitempty"`
}

type wireGroundingChunk struct {
	Web *wireGroundingWeb `json:"web,omitempty"`
}

type wireGroundingWeb struct {
	URI   string `json:"uri,omitempty"`
	Title string `json:"title,omitempty"`
}

type wireUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// ---- Error wire type ----

type wireError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// errorMessage tries to parse Google's {"error":{"message":...}} shape.
// Falls back to the raw body if parsing fails or message is empty.
func errorMessage(body []byte) string {
	var we wireError
	if err := json.Unmarshal(body, &we); err == nil && we.Error.Message != "" {
		return we.Error.Message
	}
	return string(body)
}

// ---- Embeddings wire types ----

type embedContentRequest struct {
	Model   string      `json:"model"`
	Content wireContent `json:"content"`
}

type batchEmbedRequest struct {
	Requests []embedContentRequest `json:"requests"`
}

type batchEmbedResponse struct {
	Embeddings []embeddingValues `json:"embeddings"`
}

type embeddingValues struct {
	Values []float64 `json:"values"`
}

// applyProviderOptions merges providerOptions[name] (when it is a non-empty
// map[string]any) into the already-marshaled JSON object reqBytes, entries
// from the option map winning over whatever the SDK built. Returns reqBytes
// unchanged (no unmarshal/marshal round trip) when there's nothing to
// merge, which is the common case.
func applyProviderOptions(reqBytes []byte, providerOptions map[string]any, name string) ([]byte, error) {
	opts, _ := providerOptions[name].(map[string]any)
	if len(opts) == 0 {
		return reqBytes, nil
	}
	var m map[string]any
	if err := json.Unmarshal(reqBytes, &m); err != nil {
		return nil, fmt.Errorf("geminicompat: unmarshal request for provider options merge: %w", err)
	}
	for k, v := range opts {
		m[k] = v
	}
	return json.Marshal(m)
}

// ---- Request building ----

func buildGenerateContentRequest(modelID string, call provider.Call) (generateContentRequest, error) {
	system, contents, err := convertMessages(call.Messages)
	if err != nil {
		return generateContentRequest{}, err
	}

	req := generateContentRequest{Contents: contents}
	if system != "" {
		req.SystemInstruction = &wireContent{Parts: []wirePart{{Text: system}}}
	}

	if len(call.Tools) > 0 {
		req.Tools = []wireToolDecl{{FunctionDeclarations: convertTools(call.Tools)}}
	}
	if call.ToolChoice != nil {
		req.ToolConfig = convertToolChoice(*call.ToolChoice)
	}

	gc := &wireGenerationConfig{}
	haveGC := false

	if call.ResponseFormat != nil && call.ResponseFormat.Type == "json" {
		gc.ResponseMimeType = "application/json"
		if len(call.ResponseFormat.Schema) > 0 {
			gc.ResponseSchema = stripAdditionalProperties(call.ResponseFormat.Schema)
		}
		haveGC = true
	}
	if call.MaxTokens != nil {
		gc.MaxOutputTokens = call.MaxTokens
		haveGC = true
	}
	if call.Temperature != nil {
		gc.Temperature = call.Temperature
		haveGC = true
	}
	if call.TopP != nil {
		gc.TopP = call.TopP
		haveGC = true
	}
	if call.TopK != nil {
		gc.TopK = call.TopK
		haveGC = true
	}
	if call.PresencePenalty != nil {
		gc.PresencePenalty = call.PresencePenalty
		haveGC = true
	}
	if call.FrequencyPenalty != nil {
		gc.FrequencyPenalty = call.FrequencyPenalty
		haveGC = true
	}
	if call.Seed != nil {
		gc.Seed = call.Seed
		haveGC = true
	}
	if len(call.StopSequences) > 0 {
		gc.StopSequences = call.StopSequences
		haveGC = true
	}
	if haveGC {
		req.GenerationConfig = gc
	}

	return req, nil
}

func convertMessages(msgs []provider.Message) (system string, out []wireContent, err error) {
	var systemParts []string
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleSystem:
			systemParts = append(systemParts, textContent(m.Content))

		case provider.RoleUser:
			parts, uerr := userParts(m.Content)
			if uerr != nil {
				return "", nil, uerr
			}
			out = append(out, wireContent{Role: "user", Parts: parts})

		case provider.RoleAssistant:
			parts, aerr := assistantParts(m.Content)
			if aerr != nil {
				return "", nil, aerr
			}
			out = append(out, wireContent{Role: "model", Parts: parts})

		case provider.RoleTool:
			parts, terr := toolResultParts(m.Content)
			if terr != nil {
				return "", nil, terr
			}
			out = append(out, wireContent{Role: "user", Parts: parts})

		default:
			return "", nil, fmt.Errorf("geminicompat: unsupported message role %q", m.Role)
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

func userParts(parts []provider.ContentPart) ([]wirePart, error) {
	var out []wirePart
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			out = append(out, wirePart{Text: p.Text})
		case provider.ImagePart:
			mediaType := p.MediaType
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			out = append(out, wirePart{InlineData: &wireInlineData{
				MimeType: mediaType,
				Data:     base64.StdEncoding.EncodeToString(p.Data),
			}})
		case provider.FilePart:
			mediaType := p.MediaType
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			out = append(out, wirePart{InlineData: &wireInlineData{
				MimeType: mediaType,
				Data:     base64.StdEncoding.EncodeToString(p.Data),
			}})
		default:
			return nil, fmt.Errorf("geminicompat: unsupported content part %T in user message", part)
		}
	}
	return out, nil
}

func assistantParts(parts []provider.ContentPart) ([]wirePart, error) {
	var out []wirePart
	for _, part := range parts {
		switch p := part.(type) {
		case provider.TextPart:
			out = append(out, wirePart{Text: p.Text})
		case provider.ToolCallPart:
			args, err := parsedArgs(p.Args)
			if err != nil {
				return nil, fmt.Errorf("geminicompat: parse tool call args: %w", err)
			}
			out = append(out, wirePart{FunctionCall: &wireFunctionCall{
				Name: p.Name,
				Args: args,
			}})
		case provider.SourcePart:
			// informational, not replayable — skip
		case provider.ReasoningPart:
			// informational, not replayable — skip (geminicompat has no
			// wire representation for reasoning/thinking blocks)
		default:
			return nil, fmt.Errorf("geminicompat: unsupported content part %T in assistant message", part)
		}
	}
	return out, nil
}

// parsedArgs unmarshals raw tool-call args JSON into a generic value and
// re-marshals it, so the wire "args" field is always a parsed JSON object
// (never a raw/escaped string). An empty/absent Args defaults to an empty
// object.
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

// toolResultParts converts ToolResultParts into Gemini's functionResponse
// wire shape. Unlike Anthropic/OpenAI, Gemini's functionResponse identifies
// the call it answers by tool NAME, not by call ID, so trp.Name — not
// trp.ToolCallID — is what ends up on the wire; callers must populate
// ToolResultPart.Name (Gemini requires it, and there is no ID fallback).
//
// trp.IsError is intentionally not encoded: Gemini's functionResponse has
// no dedicated error slot — the response content is always plain JSON
// regardless of whether the tool call succeeded or failed (mirrors
// providers/openai/wire.go's handling of the "tool" message content).
func toolResultParts(parts []provider.ContentPart) ([]wirePart, error) {
	var out []wirePart
	for _, part := range parts {
		trp, ok := part.(provider.ToolResultPart)
		if !ok {
			return nil, fmt.Errorf("geminicompat: unsupported content part %T in tool message", part)
		}
		out = append(out, wirePart{FunctionResponse: &wireFunctionResponse{
			Name: trp.Name,
			Response: wireFunctionResponseContents{
				Output: toolResultForWire(trp.Result),
			},
		}})
	}
	return out, nil
}

// toolResultForWire projects an ai.ToolResultContent (or
// *ai.ToolResultContent) tool result down to its Text field, since Gemini's
// functionResponse has no image slot; every other value passes through
// unchanged. Images attached via ai.ToolResultContent are silently dropped
// for this provider — see ai.ToolResultContent's doc comment.
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

func convertTools(tools []provider.ToolDef) []wireFunctionDeclaration {
	out := make([]wireFunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		out = append(out, wireFunctionDeclaration{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  stripAdditionalProperties(t.Schema),
		})
	}
	return out
}

func convertToolChoice(tc provider.ToolChoice) *wireToolConfig {
	switch tc.Mode {
	case provider.ToolChoiceAuto:
		return &wireToolConfig{FunctionCallingConfig: wireFunctionCallingConfig{Mode: "AUTO"}}
	case provider.ToolChoiceNone:
		return &wireToolConfig{FunctionCallingConfig: wireFunctionCallingConfig{Mode: "NONE"}}
	case provider.ToolChoiceRequired:
		return &wireToolConfig{FunctionCallingConfig: wireFunctionCallingConfig{Mode: "ANY"}}
	case provider.ToolChoiceTool:
		return &wireToolConfig{FunctionCallingConfig: wireFunctionCallingConfig{
			Mode:                 "ANY",
			AllowedFunctionNames: []string{tc.ToolName},
		}}
	default:
		return nil
	}
}

// stripAdditionalProperties returns raw with every "additionalProperties"
// key removed, at any nesting depth — Gemini rejects JSON Schema documents
// that include it. Returns raw unmodified if it doesn't parse as JSON.
func stripAdditionalProperties(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	b, err := json.Marshal(stripAP(v))
	if err != nil {
		return raw
	}
	return b
}

func stripAP(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "additionalProperties" {
				continue
			}
			out[k] = stripAP(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = stripAP(val)
		}
		return out
	default:
		return v
	}
}

// ---- Response conversion ----

func mapFinishReason(reason string, hasFunctionCall bool) provider.FinishReason {
	if hasFunctionCall {
		return provider.FinishToolCalls
	}
	switch reason {
	case "STOP":
		return provider.FinishStop
	case "MAX_TOKENS":
		return provider.FinishLength
	case "SAFETY":
		return provider.FinishContentFilter
	default:
		return provider.FinishOther
	}
}

func convertResponse(wr generateContentResponse, raw []byte) *provider.Response {
	resp := &provider.Response{Raw: json.RawMessage(raw)}

	if wr.UsageMetadata != nil {
		resp.Usage = provider.Usage{
			InputTokens:  wr.UsageMetadata.PromptTokenCount,
			OutputTokens: wr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  wr.UsageMetadata.TotalTokenCount,
		}
	}

	if len(wr.Candidates) == 0 {
		return resp
	}
	cand := wr.Candidates[0]

	hasFunctionCall := false
	for i, part := range cand.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			hasFunctionCall = true
			args := part.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			resp.Content = append(resp.Content, provider.ToolCallPart{
				ID:   fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, i),
				Name: part.FunctionCall.Name,
				Args: args,
			})
		case part.Text != "":
			resp.Content = append(resp.Content, provider.TextPart{Text: part.Text})
		}
	}

	for _, sp := range groundingSources(cand.GroundingMetadata) {
		resp.Content = append(resp.Content, sp)
	}

	resp.FinishReason = mapFinishReason(cand.FinishReason, hasFunctionCall)
	return resp
}

// groundingSources converts a candidate's groundingMetadata.groundingChunks
// into provider.SourceParts, one per chunk that carries a web citation.
// IDs are "source_<index>" where index is the chunk's position within
// GroundingChunks (not a global counter), matching the brief's spec.
func groundingSources(gm *wireGroundingMeta) []provider.SourcePart {
	if gm == nil {
		return nil
	}
	var out []provider.SourcePart
	for i, chunk := range gm.GroundingChunks {
		if chunk.Web == nil {
			continue
		}
		out = append(out, provider.SourcePart{
			ID:    fmt.Sprintf("source_%d", i),
			URL:   chunk.Web.URI,
			Title: chunk.Web.Title,
		})
	}
	return out
}
