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

	// TopK restricts sampling to the K highest-probability tokens at each
	// step. Supported by anthropic, geminicompat (Google/Vertex), and
	// cohere (wire field "k"). Ignored (not sent, no error) by
	// openaicompat-based providers (OpenAI's chat-completions API has no
	// top_k parameter), mistral, and bedrock — see each provider's wire.go
	// for the specific ignore comment. ProviderOptions can still reach an
	// otherwise-unsupported provider's native parameter name directly (e.g.
	// {"mistral": {"top_k": 5}}) if that provider's API happens to accept
	// it undocumented; this field only covers parameters this SDK maps by
	// name.
	TopK *int
	// PresencePenalty and FrequencyPenalty apply OpenAI-style repetition
	// penalties. Supported by openaicompat-based providers, cohere, and
	// mistral — all under the same wire names ("presence_penalty" /
	// "frequency_penalty"). Ignored by anthropic, geminicompat, and
	// bedrock — see each provider's wire.go for the specific ignore
	// comment.
	PresencePenalty  *float64
	FrequencyPenalty *float64
	// Seed requests (best-effort, provider-dependent) deterministic
	// sampling. Supported by openaicompat-based providers ("seed"), cohere
	// ("seed"), and mistral ("random_seed"). Ignored by anthropic,
	// geminicompat, and bedrock — see each provider's wire.go for the
	// specific ignore comment.
	Seed *int64

	// Reasoning is the unified reasoning/thinking request option. Providers
	// map it to their native knob: openaicompat sends Effort under
	// "reasoning_effort" (BudgetTokens has no OpenAI-wire equivalent, so
	// it's ignored there); anthropic, geminicompat, and bedrock instead
	// need a token budget, resolved via EffortBudgetTokens when
	// BudgetTokens is unset (see that function's table) — anthropic sends
	// it as "thinking":{"type":"enabled","budget_tokens":N}, geminicompat
	// as generationConfig.thinkingConfig:{"thinkingBudget":N,
	// "includeThoughts":true}, and bedrock as
	// additionalModelRequestFields.thinking (same shape as anthropic's).
	// cohere and mistral have no reasoning knob and ignore this field
	// entirely. See each provider's wire.go for the exact mapping. Effort
	// and BudgetTokens may be set together (providers that only understand
	// one use that one; BudgetTokens wins where both map to the same
	// knob). ProviderOptions still merge last and win on wire-key
	// collision.
	Reasoning *ReasoningConfig

	// Headers carries extra HTTP headers to send with the request, applied
	// AFTER the provider sets its own authentication header(s) — so an
	// entry here can never override the auth header itself: an entry whose
	// key case-insensitively matches the header the provider uses for
	// authentication (e.g. "Authorization", "x-api-key", "x-goog-api-key")
	// is silently skipped, all other entries win over anything the SDK
	// would otherwise set. Implemented by every language-model request path
	// (openaicompat, geminicompat, anthropic, cohere, mistral, bedrock).
	// Not implemented (this wave) by any embedding or media (image/speech/
	// transcription) request path.
	//
	// bedrock is a special case because requests are SigV4-signed: an entry
	// whose key case-insensitively starts with "x-amz-" is set BEFORE
	// signing, so it participates in the signature (SigV4 signs every
	// x-amz-* header present on the request); every other entry is set
	// AFTER signing, so it reaches the wire unsigned.
	Headers map[string]string

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

// ReasoningConfig is the unified reasoning/thinking request option.
// Providers map it to their native knob; see each provider's request
// builder for the exact mapping. Effort and BudgetTokens may be set
// together (providers that only understand one use that one; BudgetTokens
// wins where both map to the same knob). ProviderOptions still merge last
// and win on wire-key collision.
type ReasoningConfig struct {
	Effort       string // "", "minimal", "low", "medium", "high" — passed through, not validated
	BudgetTokens *int   // explicit thinking-token budget
}

// EffortBudgetTokens maps a ReasoningConfig.Effort value to an explicit
// thinking-token budget, for providers whose only reasoning knob is a token
// budget (anthropic, geminicompat, bedrock): "minimal"→1024, "low"→4096,
// "medium"→8192, "high"→16384. Reports false (with a zero int) for "" or
// any unrecognized effort string, so callers can omit the field entirely
// rather than send a fabricated default.
func EffortBudgetTokens(effort string) (int, bool) {
	switch effort {
	case "minimal":
		return 1024, true
	case "low":
		return 4096, true
	case "medium":
		return 8192, true
	case "high":
		return 16384, true
	default:
		return 0, false
	}
}

// ResolveBudgetTokens resolves a ReasoningConfig to a concrete thinking-token
// budget: BudgetTokens when set, else the EffortBudgetTokens mapping of
// Effort. ok is false when neither resolves.
func ResolveBudgetTokens(cfg *ReasoningConfig) (n int, ok bool) {
	if cfg == nil {
		return 0, false
	}
	if cfg.BudgetTokens != nil {
		return *cfg.BudgetTokens, true
	}
	return EffortBudgetTokens(cfg.Effort)
}
