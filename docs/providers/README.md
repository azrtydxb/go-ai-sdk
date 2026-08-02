# Provider overview

`go-ai-sdk` exposes every provider through the same `provider.LanguageModel`
/ `provider.EmbeddingModel` / `provider.ImageModel` / `provider.SpeechModel`
/ `provider.TranscriptionModel` interfaces, constructed from a per-provider
package under [`providers/`](../../providers). Nine of the sixteen
(OpenAI, Azure, Groq, xAI, DeepSeek, Cerebras, Together, Fireworks,
Perplexity) share one implementation, [`internal/openaicompat`](../../internal/openaicompat),
configured per provider by a `Config` preset; Google and Vertex AI share
[`internal/geminicompat`](../../internal/geminicompat); Anthropic, Bedrock,
Mistral, Cohere, and ElevenLabs are standalone implementations because
their wire formats diverge too far from either shared base.

Every provider reads its API key from an environment variable by default
and accepts a `With*` functional option to override it — see
[Getting started](../getting-started.md) for the install-and-first-call
quickstart. `ProviderOptions`/`ProviderMetadata` (the raw-wire-key escape
hatch used throughout the pages below) are documented once, generically, in
[Provider options](../core/provider-options.md).

## Capability matrix

✓ = supported · ✗ = not exposed by this package · ⚠ = supported with a
caveat, see that provider's page

| Provider | Chat & streaming | Tool calling | Structured output | Embeddings | Images | Speech (TTS) | Transcription (STT) |
|---|---|---|---|---|---|---|---|
| [OpenAI](openai.md) | ✓ | ✓ | ✓ native | ✓ | ✓ | ✓ | ✓ |
| [Azure OpenAI](azure.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ |
| [Groq](groq.md) | ✓ | ✓ | ✓ native | ✗ | ✗ | ✗ | ✓ |
| [xAI](xai.md) | ✓ | ✓ | ✓ native | ✗ | ✓ ⚠¹ | ✗ | ✗ |
| [DeepSeek](deepseek.md) | ✓ | ✓ | ⚠² `json_object`-only | ✗ | ✗ | ✗ | ✗ |
| [Cerebras](cerebras.md) | ✓ | ✓ | ✓ native | ✗ | ✗ | ✗ | ✗ |
| [Together](together.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ |
| [Fireworks](fireworks.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ |
| [Perplexity](perplexity.md) | ✓ | ⚠³ no live tools | ✓ native | ✗ | ✗ | ✗ | ✗ |
| [Mistral](mistral.md) | ✓ | ✓ | ⚠⁴ schema dropped | ✓ | ✗ | ✗ | ✗ |
| [Cohere](cohere.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ |
| [ElevenLabs](elevenlabs.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| [Anthropic](anthropic.md) | ✓ | ✓ | ⚠⁵ tool-mode | ✗ | ✗ | ✗ | ✗ |
| [Google](google.md) | ✓ | ✓ | ✓ native | ✓ | ✓ | ✗ | ✗ |
| [Vertex AI](vertex.md) | ✓ | ✓ | ✓ native | ✓ | ✓ | ✗ | ✗ |
| [Amazon Bedrock](bedrock.md) | ✓ | ✓ | ⚠⁵ tool-mode | ✓ | ✗ | ✗ | ✗ |

¹ xAI's image endpoint rejects the `size` parameter — see
[xAI quirks](xai.md#quirks-and-notes).
² DeepSeek's `response_format` only accepts `json_object`, never
`json_schema`; `ResponseFormat.Schema` is dropped from the wire request
(validated client-side by the `ai` core's decode step instead) — see
[DeepSeek quirks](deepseek.md#quirks-and-notes).
³ Perplexity's API does not honor tool-calling; `Call.Tools` is still
serialized onto the wire request, but the live API may reject or ignore it
— see [Perplexity quirks](perplexity.md#quirks).
⁴ Mistral's `response_format` also only ever sends `json_object`, dropping
`ResponseFormat.Schema` the same way DeepSeek does — see
[Mistral quirks](mistral.md#quirks).
⁵ Anthropic and Bedrock have no schema-constrained native JSON mode;
`ai.GenerateObject` falls back to a forced single-tool-call
("tool-mode") instead of setting `ResponseFormat` — see
[Reasoning](../core/reasoning.md) and each page's Quirks section.

"Structured output" above means schema-constrained JSON generation via
`ai.GenerateObject`/`ai.StreamObject`; every provider that returns any
text at all can still be asked to produce JSON in its prompt.

## Reasoning / extended thinking

Anthropic (`thinking`/`redacted_thinking`), Bedrock (`reasoningContent`),
and DeepSeek (`reasoning_content`) each surface a "thinking" channel,
unified as `provider.ReasoningPart` — see [Reasoning](../core/reasoning.md)
for the cross-provider mechanics and the Anthropic `budget_tokens` worked
example.

## Construction at a glance

| Provider | Env var | Default base URL | Auth |
|---|---|---|---|
| [OpenAI](openai.md) | `OPENAI_API_KEY` | `https://api.openai.com/v1` | `Authorization: Bearer` |
| [Azure OpenAI](azure.md) | `AZURE_API_KEY` (+ `AZURE_RESOURCE_NAME`) | derived from resource name | `api-key` header |
| [Groq](groq.md) | `GROQ_API_KEY` | `https://api.groq.com/openai/v1` | `Authorization: Bearer` |
| [xAI](xai.md) | `XAI_API_KEY` | `https://api.x.ai/v1` | `Authorization: Bearer` |
| [DeepSeek](deepseek.md) | `DEEPSEEK_API_KEY` | `https://api.deepseek.com/v1` | `Authorization: Bearer` |
| [Cerebras](cerebras.md) | `CEREBRAS_API_KEY` | `https://api.cerebras.ai/v1` | `Authorization: Bearer` |
| [Together](together.md) | `TOGETHER_AI_API_KEY` | `https://api.together.xyz/v1` | `Authorization: Bearer` |
| [Fireworks](fireworks.md) | `FIREWORKS_API_KEY` | `https://api.fireworks.ai/inference/v1` | `Authorization: Bearer` |
| [Perplexity](perplexity.md) | `PERPLEXITY_API_KEY` | `https://api.perplexity.ai` | `Authorization: Bearer` |
| [Mistral](mistral.md) | `MISTRAL_API_KEY` | `https://api.mistral.ai/v1` | `Authorization: Bearer` |
| [Cohere](cohere.md) | `COHERE_API_KEY` | `https://api.cohere.com/v2` | `Authorization: Bearer` |
| [ElevenLabs](elevenlabs.md) | `ELEVENLABS_API_KEY` | `https://api.elevenlabs.io` | `xi-api-key` header |
| [Anthropic](anthropic.md) | `ANTHROPIC_API_KEY` | `https://api.anthropic.com` | `x-api-key` + `anthropic-version` headers |
| [Google](google.md) | `GOOGLE_GENERATIVE_AI_API_KEY` | `https://generativelanguage.googleapis.com/v1beta` | `x-goog-api-key` header |
| [Vertex AI](vertex.md) | `GOOGLE_VERTEX_PROJECT` / `GOOGLE_VERTEX_LOCATION` / `GOOGLE_APPLICATION_CREDENTIALS` | derived from project/location | OAuth2 bearer (service account or `WithTokenSource`) |
| [Amazon Bedrock](bedrock.md) | `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` / `AWS_REGION` | derived from region | AWS SigV4 |

## Provider pages

- [OpenAI](openai.md) — the full preset: chat, embeddings, images, speech, transcription
- [Azure OpenAI](azure.md) — deployment names, `api-key` header, derived base URL
- [Groq](groq.md) — chat + Whisper transcription, no embeddings/images
- [xAI](xai.md) — chat + image generation; `size` rejected on images
- [DeepSeek](deepseek.md) — `json_object`-only structured output, `reasoning_content`
- [Cerebras](cerebras.md) — chat only, native JSON schema
- [Together](together.md) — chat + embeddings, `max_tokens` field name
- [Fireworks](fireworks.md) — chat + embeddings, `/inference/v1` base path
- [Perplexity](perplexity.md) — chat only; tools serialized but not honored live
- [Mistral](mistral.md) — standalone wire format; schema dropped from `response_format`
- [Cohere](cohere.md) — standalone v2 chat/embed API; `p` for top_p, typed SSE events
- [ElevenLabs](elevenlabs.md) — speech + transcription only, no language model
- [Anthropic](anthropic.md) — Messages API, extended thinking, tool-mode structured output
- [Google](google.md) — Gemini Developer API, grounding citations, Imagen
- [Vertex AI](vertex.md) — Gemini on Google Cloud, OAuth2/service-account auth, global location
- [Amazon Bedrock](bedrock.md) — Converse API, SigV4 signing, document attachments

## Source of truth

This matrix mirrors the `Config` presets under
[`internal/openaicompat`](../../internal/openaicompat) and
[`internal/geminicompat`](../../internal/geminicompat), and each
standalone package's exported constructors under
[`providers/`](../../providers). See each provider's own page for
file:line citations.
