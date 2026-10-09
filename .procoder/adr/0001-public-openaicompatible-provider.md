# 0001 — Public openaicompatible provider

Status: accepted
Date: 2026-10-09

## Context

Kuvryn Atlas runs fully offline against customer-hosted, OpenAI-compatible
endpoints (vLLM, TEI, Ollama, speaches). The SDK had no importable generic
provider: `internal/openaicompat` cannot be imported, and `providers/openai`
with `WithBaseURL` is a workaround that still defaults to the public internet
and always sends an Authorization header. Reranking worked only through
Cohere, Mixedbread and Voyage, while self-hosted rerankers speak either the
vLLM/OpenAI `/v1/rerank` shape or the Hugging Face TEI `/rerank` shape.
Self-hosted speech and transcription (for example MediaTek Breeze-ASR-25 and
BreezyVoice behind OpenAI-style audio endpoints) needed the same treatment.
(Issues #35 and #36.)

## Decision

Add `providers/openaicompatible`, a thin public wrapper over
`internal/openaicompat`, matching the existing preset idiom.

- Constructor: `New(baseURL string, opts ...Option) *Provider`. The base URL
  is a required positional argument with no default. An empty value is not a
  panic: every model call returns a "base URL not configured" error, the same
  idiom the shared internals already use.
- Options: `WithAPIKey` (optional; no Authorization header when empty),
  `WithHeader`/`WithHeaders`, `WithHTTPClient`, `WithName` (reported by
  `ProviderName`), plus `WithEmbeddingBatchSize`, `WithTranscriptionFormat`
  and `WithMaxTokensParam` for server quirks.
- Model accessors: `Chat`, `Embedding`, `Speech`, `Transcription`, and
  `Rerank(modelID, ...RerankOption)` with `WithRerankShape(RerankOpenAI |
  RerankTEI)`.
- No dedicated Breeze package; Breeze models are reached through
  `Transcription` and `Speech`.

Rejected: reusing the `Model`/`EmbeddingModel` accessor names of the vendor
presets (this package has more modalities, so modality names are clearer);
an env-var API key fallback (an offline-first provider should not read
ambient credentials); a default localhost URL (silent misdirection); one
rerank model that auto-detects the shape (guessing against a server is
worse than a one-option choice).

## Consequences

Easier: one supported contract for any self-hosted stack, with contract tests
against httptest servers. `internal/openaicompat.Config` gains `OmitEmptyAuth`,
`Headers` and `TranscriptionFormat`, all opt-in so existing wrappers keep
their behavior; speech now trusts a server's `audio/*` Content-Type.

Harder: the wrapper has to track the quirks of several servers (TEI's base
URL has no `/v1`, older vLLM Whisper lacks `verbose_json`). Cutting a tagged
release remains a maintainer action.
