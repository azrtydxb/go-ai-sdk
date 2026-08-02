package provider

import "encoding/json"

type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage // JSON Schema for args
}

type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceTool     ToolChoiceMode = "tool"
)

type ToolChoice struct {
	Mode     ToolChoiceMode
	ToolName string // set when Mode == ToolChoiceTool
}

type ResponseFormat struct {
	Type   string          // "text" | "json"
	Schema json.RawMessage // optional, when Type == "json"
	Name   string          // optional schema name
}

type Call struct {
	Messages       []Message
	Tools          []ToolDef
	ToolChoice     *ToolChoice
	ResponseFormat *ResponseFormat
	MaxTokens      *int
	Temperature    *float64
	TopP           *float64
	StopSequences  []string

	// ProviderOptions is an escape hatch for provider-specific parameters.
	// It is keyed by provider name (e.g. "anthropic", "openai", "azure",
	// "groq" — the value returned by the model's ProviderName(), which for
	// the OpenAI/Gemini-compatible bases is the preset's Config.Name).
	// Each provider looks up ITS OWN key; entries under other providers'
	// keys are ignored. The value under a matching key must be a
	// map[string]any (other value types are ignored); its entries are
	// shallow-merged into the top-level JSON object the SDK builds for the
	// request, AFTER the SDK builds it — so option entries win over
	// SDK-set fields (e.g. {"anthropic": {"temperature": 0.9}} overrides
	// Call.Temperature). Novel keys not otherwise exposed by this SDK
	// (e.g. {"anthropic": {"top_k": 5}}) pass through untouched. For
	// multipart-body calls (openaicompat transcription, ElevenLabs
	// transcription), entries are instead sent as extra form fields, each
	// stringified with fmt.Sprint. Setting ProviderOptions is a no-op for
	// any key that doesn't match the provider actually being called.
	ProviderOptions map[string]any
}
