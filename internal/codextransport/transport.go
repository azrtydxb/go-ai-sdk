// Package codextransport sends requests to the OpenAI Codex backend
// (chatgpt.com/backend-api/codex/responses) using the Responses API wire
// format, and parses the SSE stream back into provider.StreamPart values.
package codextransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/internal/codexauth"
	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/internal/sse"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	// defaultCodexBaseURL is the Codex backend base URL.
	defaultCodexBaseURL = "https://chatgpt.com/backend-api"
	// codexResponsesPath is the SSE endpoint.
	codexResponsesPath = "/codex/responses"
	// openaiBetaResponses is the OpenAI-Beta header for responses.
	openaiBetaResponses = "responses=experimental"
	// defaultModelID is the default model ID.
	defaultModelID = "gpt-5.4"
	// defaultOriginator is the originator header value.
	defaultOriginator = "go-ai-sdk"
	// defaultUserAgent is the default User-Agent.
	defaultUserAgent = "go-ai-sdk/codex"
)

// Config parameterizes a Codex request.
type Config struct {
	Credential      codexauth.Credential // OAuth credentials
	ModelID         string               // model ID (default "gpt-5.4")
	BaseURL         string               // backend base URL (default "https://chatgpt.com/backend-api")
	HTTPClient      *http.Client         // client (default http.DefaultClient)
	Instructions    string               // system instructions
	Headers         map[string]string    // extra headers
	ProviderOpts    map[string]any       // provider options
	ReasoningEffort string               // reasoning depth ("low", "medium", "high", "none")
	Temperature     *float64             // sampling temperature (0-2)
	MaxTokens       *int                 // max output tokens
	ToolChoice      *provider.ToolChoice // tool choice mode
}

// languageModel is a provider.LanguageModel backed by the Codex Responses API.
type languageModel struct {
	cfg Config
}

// NewModel creates a language model using the given codex credentials.
func NewModel(cred codexauth.Credential) provider.LanguageModel {
	return &languageModel{
		cfg: Config{
			Credential: cred,
			ModelID:    defaultModelID,
			BaseURL:    defaultCodexBaseURL,
		},
	}
}

// NewModelWithOptions creates a language model with additional options.
func NewModelWithOptions(cfg Config) provider.LanguageModel {
	if cfg.ModelID == "" {
		cfg.ModelID = defaultModelID
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultCodexBaseURL
	}
	return &languageModel{cfg: cfg}
}

func (m *languageModel) ModelID() string      { return m.cfg.ModelID }
func (m *languageModel) ProviderName() string { return "openai-codex" }
func (m *languageModel) Capabilities() provider.Capabilities {
	return provider.Capabilities{NativeJSON: false}
}

// Generate makes a non-streaming Codex request.
func (m *languageModel) Generate(ctx context.Context, call provider.Call) (*provider.Response, error) {
	if m.cfg.Credential.Access == "" {
		return nil, errors.New("codextransport: no credentials configured")
	}
	req, err := m.buildRequest(ctx, call, false)
	if err != nil {
		return nil, err
	}
	req.Header.Del("Accept")

	client := m.cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codextransport: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codextransport: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	return parseNonStreamingResponse(body)
}

// Stream sends a Codex streaming request.
func (m *languageModel) Stream(ctx context.Context, call provider.Call) (provider.StreamResponse, error) {
	if m.cfg.Credential.Access == "" {
		return nil, errors.New("codextransport: no credentials configured")
	}
	req, err := m.buildRequest(ctx, call, true)
	if err != nil {
		return nil, err
	}

	client := m.cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codextransport: request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("codextransport: read error response: %w", readErr)
		}
		return nil, apiError(resp, body)
	}

	return &streamResponse{body: resp.Body, modelID: m.cfg.ModelID}, nil
}

func (m *languageModel) buildRequest(ctx context.Context, call provider.Call, stream bool) (*http.Request, error) {
	cfg := m.cfg

	input, err := convertMessages(call.Messages)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"model":               cfg.ModelID,
		"store":               false,
		"stream":              stream,
		"input":               input,
		"text":                map[string]string{"verbosity": "low"},
		"include":             []string{"reasoning.encrypted_content"},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
	}

	if cfg.Instructions != "" {
		body["instructions"] = cfg.Instructions
	}
	effort := cfg.ReasoningEffort
	if call.Reasoning != nil && call.Reasoning.Effort != "" {
		effort = call.Reasoning.Effort
	}
	if effort != "" {
		body["reasoning"] = map[string]string{
			"effort":  effort,
			"summary": "auto",
		}
	}
	if cfg.Temperature != nil {
		body["temperature"] = *cfg.Temperature
	}
	if cfg.MaxTokens != nil {
		body["max_output_tokens"] = *cfg.MaxTokens
	}

	if len(call.Tools) > 0 {
		body["tools"] = convertTools(call.Tools)
	}
	if cfg.ToolChoice != nil {
		body["tool_choice"] = toolChoiceString(cfg.ToolChoice)
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("codextransport: marshal request: %w", err)
	}
	bodyJSON, err = providerutil.ApplyProviderOptions(bodyJSON, cfg.ProviderOpts, "openai-codex")
	if err != nil {
		return nil, fmt.Errorf("codextransport: apply provider options: %w", err)
	}

	url := cfg.BaseURL
	if !strings.HasSuffix(url, "/") && !strings.HasSuffix(url, codexResponsesPath) {
		url += codexResponsesPath
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("codextransport: build request: %w", err)
	}

	cred := cfg.Credential
	req.Header.Set("Authorization", "Bearer "+cred.Access)
	req.Header.Set("chatgpt-account-id", cred.AccountID)
	req.Header.Set("originator", defaultOriginator)
	req.Header.Set("OpenAI-Beta", openaiBetaResponses)
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// Apply extra headers, protecting auth headers.
	authHeaders := map[string]bool{
		"authorization":      true,
		"chatgpt-account-id": true,
	}
	for k, v := range call.Headers {
		if authHeaders[strings.ToLower(k)] {
			continue
		}
		req.Header.Set(k, v)
	}
	if m.cfg.Headers != nil {
		for k, v := range m.cfg.Headers {
			if authHeaders[strings.ToLower(k)] {
				continue
			}
			req.Header.Set(k, v)
		}
	}
	httpheader.Apply(req, call.Headers, "authorization")

	return req, nil
}

// convertMessages converts provider.Messages to the Codex Responses API input.
func convertMessages(msgs []provider.Message) ([]map[string]any, error) {
	var result []map[string]any
	for _, msg := range msgs {
		switch msg.Role {
		case provider.RoleSystem:
			for _, part := range msg.Content {
				if tp, ok := part.(provider.TextPart); ok {
					result = append(result, map[string]any{
						"type":    "message",
						"role":    "system",
						"content": []map[string]string{{"type": "input_text", "text": tp.Text}},
						"status":  "completed",
					})
				}
			}
		case provider.RoleUser:
			result = append(result, userMessageToInputItem(msg))
		case provider.RoleAssistant:
			result = append(result, assistantMessageToOutputItem(msg))
		case provider.RoleTool:
			item, err := toolResultToOutputItem(msg)
			if err != nil {
				return nil, err
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func userMessageToInputItem(msg provider.Message) map[string]any {
	items := make([]map[string]any, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case provider.TextPart:
			items = append(items, map[string]any{
				"type": "input_text", "text": p.Text,
			})
		case provider.ImagePart:
			if p.URL != "" {
				items = append(items, map[string]any{
					"type":      "input_image",
					"image_url": p.URL,
					"detail":    "auto",
				})
			}
		}
	}
	return map[string]any{
		"type": "message", "role": "user", "content": items, "status": "completed",
	}
}

func assistantMessageToOutputItem(msg provider.Message) map[string]any {
	items := make([]map[string]any, 0, len(msg.Content))
	for _, part := range msg.Content {
		switch p := part.(type) {
		case provider.TextPart:
			items = append(items, map[string]any{
				"type": "output_text", "text": p.Text,
			})
		case provider.ToolCallPart:
			items = append(items, map[string]any{
				"type":      "function_call",
				"id":        p.ID,
				"call_id":   p.ID,
				"name":      p.Name,
				"arguments": string(p.Args),
			})
		}
	}
	return map[string]any{
		"type": "message", "role": "assistant", "content": items, "status": "completed",
	}
}

func toolResultToOutputItem(msg provider.Message) (map[string]any, error) {
	for _, part := range msg.Content {
		var trp provider.ToolResultPart
		switch p := part.(type) {
		case provider.ToolResultPart:
			trp = p
		case *provider.ToolResultPart:
			if p == nil {
				continue
			}
			trp = *p
		default:
			continue
		}

		result, err := json.Marshal(trp.Result)
		if err != nil {
			return nil, fmt.Errorf("codextransport: marshal tool result: %w", err)
		}
		return map[string]any{
			"type":    "function_call_result",
			"call_id": trp.ToolCallID,
			"result":  string(result),
			"status":  "completed",
		}, nil
	}
	return nil, errors.New("codextransport: tool message missing ToolResultPart")
}

func convertTools(tools []provider.ToolDef) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, t := range tools {
		item := map[string]any{
			"type":        "function",
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Schema,
			"strict":      t.Strict,
		}
		if len(t.InputExamples) > 0 {
			item["input_examples"] = t.InputExamples
		}
		result[i] = item
	}
	return result
}

func toolChoiceString(tc *provider.ToolChoice) string {
	if tc == nil {
		return "auto"
	}
	switch tc.Mode {
	case provider.ToolChoiceNone:
		return "none"
	case provider.ToolChoiceRequired:
		return "required"
	case provider.ToolChoiceTool:
		return tc.ToolName
	default:
		return "auto"
	}
}

func apiError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), providerutil.ErrorMessage(body))
}

// ---- Non-streaming response parsing ----

type responseCompletion struct {
	ID                string         `json:"id"`
	Model             string         `json:"model"`
	Output            []outputItem   `json:"output"`
	Usage             *responseUsage `json:"usage"`
	StopReason        string         `json:"stop_reason"`
	Status            string         `json:"status"`
	SystemFingerprint string         `json:"system_fingerprint"`
}

type outputItem struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"`
	Role       string        `json:"role,omitempty"`
	Content    []contentPart `json:"content"`
	StopReason string        `json:"stop_reason,omitempty"`
}

type contentPart struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func parseNonStreamingResponse(body []byte) (*provider.Response, error) {
	var r responseCompletion
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("codextransport: decode response: %w", err)
	}

	parts, finishReason, usage := parseResponseOutput(r.Output, r.Usage, r.StopReason)
	return &provider.Response{
		Content:      parts,
		FinishReason: finishReason,
		Usage:        usage,
		Raw:          json.RawMessage(body),
		ProviderMetadata: map[string]any{
			"openai-codex": map[string]any{
				"id":          r.ID,
				"model":       r.Model,
				"stop_reason": r.StopReason,
			},
		},
	}, nil
}

func parseResponseOutput(items []outputItem, usage *responseUsage, stopReason string) ([]provider.ContentPart, provider.FinishReason, provider.Usage) {
	var parts []provider.ContentPart
	var finishReason provider.FinishReason
	var resultUsage provider.Usage

	if usage != nil {
		resultUsage.InputTokens = usage.InputTokens
		resultUsage.OutputTokens = usage.OutputTokens
		resultUsage.TotalTokens = usage.TotalTokens
		resultUsage.CachedInputTokens = usage.CachedInputTokensRead
	}

	for _, item := range items {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				switch c.Type {
				case "output_text":
					parts = append(parts, provider.TextPart{Text: c.Text})
				case "function_call":
					parts = append(parts, provider.ToolCallPart{
						ID:   c.Name,
						Name: c.Name,
						Args: json.RawMessage(c.Arguments),
					})
				}
			}
		case "function_call_result":
			if len(items) > 0 {
				parts = append(parts, provider.ToolResultPart{
					ToolCallID: items[0].ID,
					Result:     item.Content,
					IsError:    false,
				})
			}
		}
	}

	if len(items) > 0 {
		finishReason = mapStopReason(stopReason)
	}
	return parts, finishReason, resultUsage
}

// ---- Streaming SSE parsing ----

type streamResponse struct {
	body    io.ReadCloser
	err     error
	used    bool
	closed  bool
	modelID string
}

func (s *streamResponse) Parts() iter.Seq[provider.StreamPart] {
	return func(yield func(provider.StreamPart) bool) {
		if s.used {
			return
		}
		s.used = true

		var (
			currentItemType string
			currentItemID   string
			toolCallID      string
			toolCallName    string
			toolCallArgs    strings.Builder
			textBuilder     strings.Builder
			reasoningText   strings.Builder
			reasoningSig    string
			finishReason    provider.FinishReason
			usage           provider.Usage
			haveFinish      bool
			systemFP        string
		)

		for ev, err := range sse.Scan(s.body) {
			if err != nil {
				s.err = fmt.Errorf("codextransport: stream read: %w", err)
				return
			}

			data := strings.TrimSpace(ev.Data)
			if data == "" {
				continue
			}

			var event streamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				s.err = fmt.Errorf("codextransport: decode stream event: %w", err)
				return
			}

			switch event.Type {
			case "response.created":
				if event.Response != nil {
					systemFP = event.Response.SystemFingerprint
				}

			case "response.output_item.added":
				currentItemType = event.Item.Type
				currentItemID = event.Item.ID
				if currentItemType == "function_call" {
					toolCallID = currentItemID
					toolCallName = event.Item.Name
				}

			case "response.delta":
				delta := decodeStreamDelta(event.Delta)
				switch delta.Type {
				case "delta":
					if delta.Text != "" {
						textBuilder.WriteString(delta.Text)
						if !yield(provider.TextDelta{Text: delta.Text}) {
							return
						}
					}
				case "input_delta":
					if delta.Text != "" && toolCallID != "" {
						toolCallArgs.WriteString(delta.Text)
						if !yield(provider.ToolCallDelta{
							ID:        toolCallID,
							Name:      toolCallName,
							ArgsDelta: delta.Text,
						}) {
							return
						}
					}
				case "reasoning_delta":
					if delta.Text != "" {
						reasoningText.WriteString(delta.Text)
						if !yield(provider.ReasoningDelta{Text: delta.Text}) {
							return
						}
					}
				case "signature_delta":
					reasoningSig += delta.Text
				}

			case "response.output_text.delta":
				delta := decodeStreamDelta(event.Delta)
				if delta.Text != "" {
					textBuilder.WriteString(delta.Text)
					if !yield(provider.TextDelta{Text: delta.Text}) {
						return
					}
				}

			case "response.function_call_arguments.delta":
				delta := decodeStreamDelta(event.Delta)
				if delta.Text != "" && toolCallID != "" {
					toolCallArgs.WriteString(delta.Text)
					if !yield(provider.ToolCallDelta{ID: toolCallID, Name: toolCallName, ArgsDelta: delta.Text}) {
						return
					}
				}

			case "response.function_call_arguments.done":
				if event.Arguments != "" && toolCallID != "" {
					prev := toolCallArgs.String()
					toolCallArgs.Reset()
					toolCallArgs.WriteString(event.Arguments)
					if strings.HasPrefix(event.Arguments, prev) {
						if delta := strings.TrimPrefix(event.Arguments, prev); delta != "" {
							if !yield(provider.ToolCallDelta{ID: toolCallID, Name: toolCallName, ArgsDelta: delta}) {
								return
							}
						}
					}
				}

			case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
				delta := decodeStreamDelta(event.Delta)
				if delta.Text != "" {
					reasoningText.WriteString(delta.Text)
					if !yield(provider.ReasoningDelta{Text: delta.Text}) {
						return
					}
				}

			case "response.output_item.done":
				if currentItemType == "function_call" && toolCallID != "" {
					args := toolCallArgs.String()
					if args == "" {
						args = "{}"
					}
					end := provider.ToolCallEnd{
						Call: provider.ToolCallPart{
							ID:   toolCallID,
							Name: toolCallName,
							Args: json.RawMessage(args),
						},
					}
					if !yield(end) {
						return
					}
				}
				textBuilder.Reset()
				toolCallArgs.Reset()

				currentItemType = ""
				currentItemID = ""
				toolCallID = ""
				toolCallName = ""

			case "response.completed", "response.done", "response.incomplete":
				status := event.StopReason
				respUsage := event.Usage
				if event.Response != nil {
					status = event.Response.Status
					if status == "" {
						status = event.Response.StopReason
					}
					if event.Response.Usage != nil {
						respUsage = event.Response.Usage
					}
				}
				finishReason = mapStopReason(status)
				haveFinish = true
				if respUsage != nil {
					usage.InputTokens = respUsage.InputTokens
					usage.OutputTokens = respUsage.OutputTokens
					usage.TotalTokens = respUsage.TotalTokens
					usage.CachedInputTokens = respUsage.CachedInputTokensRead
				}

			case "response.failed":
				s.err = fmt.Errorf("codextransport: response failed: %s", event.ErrorMessage)
				return
			}
		}

		// Emit reasoning end with signature.
		if reasoningText.Len() > 0 {
			if !yield(provider.ReasoningEnd{
				Part: provider.ReasoningPart{
					Text:      reasoningText.String(),
					Signature: reasoningSig,
				},
			}) {
				return
			}
		}

		if haveFinish {
			if !yield(provider.FinishPart{
				Reason:           finishReason,
				Usage:            usage,
				ProviderMetadata: s.metadata(systemFP),
			}) {
				return
			}
			return
		}

		s.err = errors.New("codextransport: stream ended without finish event")
	}
}

func (s *streamResponse) Err() error { return s.err }

func (s *streamResponse) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.body.Close()
}

func (s *streamResponse) metadata(fp string) map[string]any {
	if fp == "" {
		return nil
	}
	return map[string]any{
		"openai-codex": map[string]any{"system_fingerprint": fp},
	}
}

// ---- Stream event wire types ----

type streamEvent struct {
	Type         string              `json:"type"`
	Response     *responseCompletion `json:"response,omitempty"`
	Delta        json.RawMessage     `json:"delta,omitempty"`
	Arguments    string              `json:"arguments,omitempty"`
	Item         streamItem          `json:"item,omitempty"`
	StopReason   string              `json:"stop_reason,omitempty"`
	ErrorMessage string              `json:"error_message,omitempty"`
	Usage        *responseUsage      `json:"usage,omitempty"`
}

type deltaEvent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func decodeStreamDelta(raw json.RawMessage) deltaEvent {
	if len(raw) == 0 {
		return deltaEvent{}
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return deltaEvent{Text: text}
	}
	var delta deltaEvent
	if err := json.Unmarshal(raw, &delta); err == nil {
		return delta
	}
	return deltaEvent{}
}

type streamItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type responseUsage struct {
	InputTokens            int `json:"input_tokens"`
	OutputTokens           int `json:"output_tokens"`
	TotalTokens            int `json:"total_tokens"`
	CachedInputTokensRead  int `json:"cached_input_tokens_read"`
	CachedInputTokensWrite int `json:"cached_input_tokens_write"`
}

func mapStopReason(s string) provider.FinishReason {
	switch s {
	case "stop", "completed":
		return provider.FinishStop
	case "length", "incomplete":
		return provider.FinishLength
	case "content_filter":
		return provider.FinishContentFilter
	case "tool_calls":
		return provider.FinishToolCalls
	default:
		return provider.FinishOther
	}
}
