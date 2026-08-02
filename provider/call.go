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
	// It is reserved for future use: as of v0.1 none of the built-in
	// LanguageModel implementations (OpenAI, Anthropic, Google) read this
	// field, so setting it is currently a silent no-op.
	ProviderOptions map[string]any
}
