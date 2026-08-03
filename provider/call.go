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
