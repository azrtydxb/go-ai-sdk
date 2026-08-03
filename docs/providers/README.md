# Provider overview

`go-ai-sdk` exposes every provider through the same `provider.LanguageModel`
/ `provider.EmbeddingModel` / `provider.ImageModel` / `provider.SpeechModel`
/ `provider.TranscriptionModel` interfaces, constructed from a per-provider
package under [`providers/`](../../providers), 25 in total. Nine of them
(OpenAI, Azure, Groq, xAI, DeepSeek, Cerebras, Together, Fireworks,
Perplexity) share one implementation, [`internal/openaicompat`](../../internal/openaicompat),
configured per provider by a `Config` preset; Google and Vertex AI share
[`internal/geminicompat`](../../internal/geminicompat); Anthropic, Bedrock,
Mistral, Cohere, ElevenLabs, fal, Replicate, Luma, Deepgram, LMNT, Hume,
AssemblyAI, Gladia, and Rev.ai are standalone implementations because their
wire formats diverge too far from either shared base (or, for the nine
media-only providers, because they implement only a single media capability
with no shared text-model base to build on).

Every provider reads its API key from an environment variable by default
and accepts a `With*` functional option to override it — see
[Getting started](../getting-started.md) for the install-and-first-call
quickstart. `ProviderOptions`/`ProviderMetadata` (the raw-wire-key escape
hatch used throughout the pages below) are documented once, generically, in
[Provider options](../core/provider-options.md).

## Capability matrix

<!-- Canonical capability matrix. README.md and docs/core/media.md summarize this; update all three together. -->

✓ = supported · ✗ = not exposed by this package · ⚠ = supported with a
caveat, see that provider's page

| Provider | Chat & streaming | Tool calling | Structured output | Embeddings | Reranking | Images | Speech (TTS) | Transcription (STT) |
|---|---|---|---|---|---|---|---|---|
| [OpenAI](openai.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✓ | ✓ | ✓ |
| [Azure OpenAI](azure.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ | ✗ |
| [Groq](groq.md) | ✓ | ✓ | ✓ native | ✗ | ✗ | ✗ | ✗ | ✓ |
| [xAI](xai.md) | ✓ | ✓ | ✓ native | ✗ | ✗ | ✓ ⚠¹ | ✗ | ✗ |
| [DeepSeek](deepseek.md) | ✓ | ✓ | ⚠² `json_object`-only | ✗ | ✗ | ✗ | ✗ | ✗ |
| [Cerebras](cerebras.md) | ✓ | ✓ | ✓ native | ✗ | ✗ | ✗ | ✗ | ✗ |
| [Together](together.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ | ✗ |
| [Fireworks](fireworks.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✗ | ✗ | ✗ |
| [Perplexity](perplexity.md) | ✓ | ⚠³ no live tools | ✓ native | ✗ | ✗ | ✗ | ✗ | ✗ |
| [Mistral](mistral.md) | ✓ | ✓ | ⚠⁴ schema dropped | ✓ | ✗ | ✗ | ✗ | ✗ |
| [Cohere](cohere.md) | ✓ | ✓ | ✓ native | ✓ | ✓ | ✗ | ✗ | ✗ |
| [ElevenLabs](elevenlabs.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✓ |
| [Anthropic](anthropic.md) | ✓ | ✓ | ⚠⁵ tool-mode | ✗ | ✗ | ✗ | ✗ | ✗ |
| [Google](google.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✓ | ✗ | ✗ |
| [Vertex AI](vertex.md) | ✓ | ✓ | ✓ native | ✓ | ✗ | ✓ | ✗ | ✗ |
| [Amazon Bedrock](bedrock.md) | ✓ | ✓ | ⚠⁵ tool-mode | ✓ | ✗ | ✗ | ✗ | ✗ |
| [fal](fal.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ |
| [Replicate](replicate.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ |
| [Luma](luma.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ | ✗ |
| [Deepgram](deepgram.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| [LMNT](lmnt.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ |
| [Hume](hume.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ | ✗ |
| [AssemblyAI](assemblyai.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| [Gladia](gladia.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |
| [Rev.ai](revai.md) | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✗ | ✓ |

Reranking (`ai.Rerank`, `provider.RerankingModel`) is Cohere-only this wave;
Voyage and Mixedbread are planned alongside their providers in a later wave
— see [Reranking](../core/embeddings.md#reranking) and
[Cohere § Reranking](cohere.md#reranking).

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
example. Requesting more (or less) reasoning effort is likewise unified
across providers, via `GenerateTextOpts.Reasoning`/`provider.Call.Reasoning`
— openaicompat-based providers map it to `reasoning_effort`; Anthropic,
Google/Vertex AI, and Bedrock map it to a token budget (an explicit one, or
resolved from an effort level via `provider.EffortBudgetTokens`); Cohere and
Mistral have no reasoning knob and ignore it. See
[Reasoning § Requesting reasoning](../core/reasoning.md#requesting-reasoning-generatetextoptsreasoning)
for the full per-provider mapping table.

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
| [fal](fal.md) | `FAL_API_KEY` (falls back to `FAL_KEY`) | `https://fal.run` | `Authorization: Key` header |
| [Replicate](replicate.md) | `REPLICATE_API_TOKEN` | `https://api.replicate.com` | `Authorization: Bearer` |
| [Luma](luma.md) | `LUMA_API_KEY` | `https://api.lumalabs.ai` | `Authorization: Bearer` |
| [Deepgram](deepgram.md) | `DEEPGRAM_API_KEY` | `https://api.deepgram.com` | `Authorization: Token` header |
| [LMNT](lmnt.md) | `LMNT_API_KEY` | `https://api.lmnt.com` | `X-API-Key` header |
| [Hume](hume.md) | `HUME_API_KEY` | `https://api.hume.ai` | `X-Hume-Api-Key` header |
| [AssemblyAI](assemblyai.md) | `ASSEMBLYAI_API_KEY` | `https://api.assemblyai.com` | `authorization` header (no `Bearer` prefix) |
| [Gladia](gladia.md) | `GLADIA_API_KEY` | `https://api.gladia.io` | `x-gladia-key` header |
| [Rev.ai](revai.md) | `REVAI_API_KEY` (falls back to `REV_AI_API_KEY`) | `https://api.rev.ai` | `Authorization: Bearer` header |

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
- [fal](fal.md) — synchronous `fal.run` image generation, `Key` auth header
- [Replicate](replicate.md) — synchronous (`Prefer: wait`) predictions API, `input`-nested options
- [Luma](luma.md) — asynchronous Dream Machine image generation, poll-until-terminal
- [Deepgram](deepgram.md) — `/v1/listen` transcription, raw-audio request body, query-param options
- [LMNT](lmnt.md) — text-to-speech, `X-API-Key` auth header
- [Hume](hume.md) — Octave text-to-speech, base64-encoded JSON audio response
- [AssemblyAI](assemblyai.md) — asynchronous transcription: upload → create → poll
- [Gladia](gladia.md) — asynchronous transcription: upload → create → poll, `x-gladia-key` auth header
- [Rev.ai](revai.md) — asynchronous transcription: multipart create → poll → fetch structured transcript

## Live-testing status

Every provider in this SDK — all 25, including the nine media-only
providers added most recently (fal, Replicate, Luma, Deepgram, LMNT, Hume,
AssemblyAI, Gladia, Rev.ai) — is verified only against recorded, documented
wire formats: unit tests run each provider's HTTP client against an
`httptest` server that replays fixture request/response bodies shaped to
match that provider's published API docs. **None of the 25 providers have
been smoke-tested against a live upstream API yet.**

The nine media-only providers (fal, Replicate, Luma, Deepgram, LMNT, Hume,
AssemblyAI, Gladia, Rev.ai) are the **highest priority** for live
verification: they're the least-battle-tested implementations in the SDK,
each page above carries a "⚠ Not yet verified against the live API" note,
and their corresponding package doc comments state the same thing. Live
verification against real API keys should happen before relying on any of
the nine in production.

## Source of truth

This matrix mirrors the `Config` presets under
[`internal/openaicompat`](../../internal/openaicompat) and
[`internal/geminicompat`](../../internal/geminicompat), and each
standalone package's exported constructors under
[`providers/`](../../providers). See each provider's own page for
file:line citations.
