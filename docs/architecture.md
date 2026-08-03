# Architecture

This page is for contributors extending `go-ai-sdk` — adding a provider,
adding a capability, or just understanding why the layers are split the
way they are. For using the SDK, start at [Getting started](getting-started.md);
for the original rationale and decisions log, see the
[design spec](superpowers/specs/2026-08-02-go-ai-sdk-design.md).

## The three layers

```
github.com/azrtydxb/go-ai-sdk
├── ai/              Layer 3: high-level API — GenerateText, StreamText,
│                    GenerateObject, Embed, tools, middleware, registry
├── provider/        Layer 1: the spec — interfaces + unified types every
│   └── providertest/  provider implements, plus the shared conformance suite
├── providers/       Layer 2: one package per vendor (openai, anthropic,
│   ├── openai/        google, groq, ...), each implementing the Layer 1
│   ├── anthropic/      interfaces against that vendor's actual wire format
│   ├── groq/            (groq/xai/deepseek/... are thin presets — see below)
│   └── ...
├── internal/        Shared plumbing not part of the public API: SSE
│   ├── openaicompat/   parsing, retry/backoff, JSON Schema reflection,
│   ├── geminicompat/    SigV4 signing, Google auth, a client-only RFC 6455
│   ├── websocket/       WebSocket client (underlying the streaming-
│   └── ...              transcription/realtime features — see
│                        docs/core/media.md), and the two compat bases
│                        (openaicompat, geminicompat — see below)
├── mcp/             MCP client, independent of the above (produces
│                    []ai.Tool, doesn't touch LanguageModel)
└── examples/        Runnable, env-guarded example programs, one per feature
```

Dependency direction is strictly downward: `ai` depends on `provider`;
`providers/*` depend on `provider` (and, for OpenAI-compatible/Gemini-
compatible vendors, on the relevant `internal/*compat` package); `provider`
depends on nothing else in this module. Nothing in `provider` or `providers/*`
imports `ai`, so the spec and its implementations stay agnostic of the
orchestration built on top of them — a `provider.LanguageModel` is usable
standalone, without ever calling into `ai.GenerateText`.

### Layer 1: `provider` — the spec

Interfaces and unified types only, no orchestration logic. The two model
interfaces every provider implements at minimum:

```go
type LanguageModel interface {
	Generate(ctx context.Context, call Call) (*Response, error)
	Stream(ctx context.Context, call Call) (StreamResponse, error)
	ModelID() string
	ProviderName() string
	Capabilities() Capabilities
}

type EmbeddingModel interface {
	Embed(ctx context.Context, values []string) (*EmbeddingResponse, error)
	MaxBatchSize() int
	ModelID() string
	ProviderName() string
}
```

`Call`, `Response`, `Message`/`ContentPart`, `StreamPart`, `ToolDef`,
`FinishReason`, `Usage` — the whole vocabulary every provider translates
its wire format into and out of — live here. `provider` never imports
`ai`, and never imports a specific vendor's package: it's the contract
both sides are written against, not a superclass either depends on.

### Layer 2: `providers/*` — implementations

One package per vendor, each satisfying `provider.LanguageModel` (and,
where supported, `provider.EmbeddingModel`/`ImageModel`/`SpeechModel`/
`TranscriptionModel`) against that vendor's actual HTTP API. Three
implementation shapes exist in practice:

- **Thin presets over `openaicompat`** — OpenAI, Azure, Groq, xAI,
  DeepSeek, Cerebras, Together, Fireworks, Perplexity: these vendors'
  APIs are OpenAI chat-completions compatible, so each provider package
  is little more than a `Config{Name, BaseURL, ...}` value handed to
  `openaicompat.NewLanguageModel`, plus vendor-specific option wiring
  (env var name, default base URL, auth header shape). OpenAI is the
  full preset (chat, embeddings, images, speech, transcription); the
  others configure a subset.
- **Presets over `geminicompat`** — Google and Vertex AI: Google is the
  full implementation `geminicompat` was extracted from (per
  `internal/geminicompat`'s doc comment), and Vertex AI configures the
  same base with its own auth and base-URL shape.
- **Full, standalone implementations** — Anthropic, Mistral, Cohere,
  Bedrock, ElevenLabs: each speaks its vendor's wire format directly
  (own request/response structs, own SSE or event-stream framing, own
  auth) because it diverges too far from either shared base.

### Compat bases: `internal/openaicompat` and `internal/geminicompat`

Both packages exist because the OpenAI chat-completions and Google
Generative Language wire formats are each reused, close to verbatim, by
multiple vendors. Rather than duplicating request-building, SSE parsing,
tool-call accumulation, and error mapping per vendor, each compat package
implements `provider.LanguageModel` (and `EmbeddingModel` where
applicable) once, parameterized by a `Config`:

```go
// internal/openaicompat
type Config struct {
	Name       string // provider.LanguageModel.ProviderName() value
	BaseURL    string
	NativeJSON bool
	// auth, max-tokens field name, response-format quirks, ...
}

// internal/geminicompat
type Config struct {
	Name        string
	HTTPClient  *http.Client
	EndpointFor func(modelID, method string) string
	Authorize   func(ctx context.Context, req *http.Request) error
	EmbedBatch  int
}
```

A vendor preset (e.g. `providers/groq`) constructs a `Config` and hands it
to `openaicompat.NewLanguageModel(cfg, modelID)`; the returned value
already satisfies `provider.LanguageModel` in full. This is composition,
not inheritance — `internal/openaicompat`/`internal/geminicompat` know
nothing about any specific vendor package; a vendor package is free to
diverge from the base entirely (as `openai` itself effectively does,
being the source the base was extracted from) if its wire format stops
matching.

### Layer 3: `ai` — the high-level API

`GenerateText`/`StreamText`, `GenerateObject[T]`/`StreamObject[T]`,
`Embed`/`EmbedMany`, `GenerateImage`/`GenerateSpeech`/`Transcribe`, tools
(`NewTool`, the multi-step loop, `RepairToolCall`), middleware
(`ExtractReasoningMiddleware`, `SimulateStreamingMiddleware`,
`DefaultSettingsMiddleware`, `TelemetryMiddleware`), and the provider
`Registry`. Everything here is built entirely on the `provider.LanguageModel`/
`EmbeddingModel`/... interfaces — it never reaches into a specific
`providers/*` package's internals, which is what makes middleware
(themselves just `provider.LanguageModel` wrapping another
`provider.LanguageModel`) and the registry's model resolution possible
without special-casing any vendor.

## The conformance suite: `provider/providertest`

Every `provider.LanguageModel` implementation should pass the same
behavioral matrix, run via `providertest.Run(t, providertest.Config{Model,
ProviderName})`. The philosophy: correctness for a `LanguageModel` isn't
"does it call the right HTTP endpoint," it's "does the *shape* of what
comes back through the `provider` interfaces match the contract" —
`Generate`/`Stream` return the unified `Response`/`StreamPart` types,
regardless of how different the underlying wire format is. `providertest`
tests exactly that shape, against a fixture HTTP server each provider's
own test file stands up, keyed off the last user message's text:

| Scenario key | Fixture must return |
|---|---|
| `"simple"` | Text `"Hello from <provider>!"`, non-zero usage, `FinishStop` |
| `"tool"` | One tool call, name `"get_weather"`, args `{"city":"Ghent"}`, `FinishToolCalls` |
| `"stream simple"` | The text `"Hello!"` streamed in ≥2 `TextDelta` chunks, then a single `FinishPart` (`FinishStop`, non-zero usage) |
| `"stream tool"` | One complete tool call as streamed deltas + a `ToolCallEnd` |
| `"fail 429"` | HTTP 429 (any body) → `*ai.APICallError{StatusCode: 429, Retryable: true}` |
| `"fail 400"` | HTTP 400 (any body) → `*ai.APICallError{StatusCode: 400, Retryable: false}` |

Plus two scenario-independent subtests: `Cancel` and `Cancel/Stream`,
asserting a pre-cancelled `context.Context` surfaces `context.Canceled`
from `Generate`/`Stream` respectively. The suite is deliberately narrow —
it isn't a mock of the vendor's full API surface, just enough scenarios to
pin down every `provider.StreamPart`/`Response` shape a caller in `ai`
relies on. Wire-format specifics (request shape, header auth, provider
options merging, reasoning/thinking, provider metadata) are covered by
each provider package's own tests, not by `providertest`.

## `StreamResponse` disciplines

`provider.StreamResponse` — `Parts() iter.Seq[StreamPart]`, `Err() error`,
`Close() error` — is the interface every streaming implementation must
honor, and every implementation (compat-base or standalone) follows the
same rules, whether or not `providertest` checks them directly:

- **`Parts()` is single-use.** Calling it a second time after the
  sequence has been exhausted or abandoned yields nothing — implementations
  guard this with a `used`/similar flag (see `openaicompat`'s
  `streamResponse.used`).
- **Yield rules follow the wire, not a fixed schedule.** A part is only
  yielded when the underlying stream actually produces the corresponding
  event — `TextDelta` per content fragment, `ToolCallDelta` per tool-call
  argument fragment (`Name` may repeat across fragments for the same ID;
  consumers must treat repeats as idempotent), `ReasoningDelta`/
  `ReasoningEnd` per thinking fragment/block, `SourceEvent` per whole
  citation (sources arrive complete — no delta form).
- **Exactly one `FinishPart`, or none.** A well-formed stream yields
  precisely one `FinishPart` before ending; a stream that errors mid-flight
  yields zero and reports the failure via `Err()` instead. Nothing ever
  synthesizes a second `FinishPart`, and nothing fabricates one to paper
  over a truncated stream.
- **The truncation rule.** When a stream's transport ends without a clean
  terminator (`openaicompat`'s case: the SSE connection closes without a
  `data: [DONE]` sentinel), the discipline branches on whether a
  finish-reason-bearing chunk was ever seen: if yes, treat the stream as
  well-formed-enough and still yield the single `FinishPart` (some
  proxies drop the trailing sentinel after forwarding the real payload);
  if no chunk with a finish reason ever arrived, the stream was truncated
  mid-response — yield **no** `FinishPart` and set `Err()` instead. "Zero
  is preferable to a fabricated one" (see `openaicompat/language_model.go`'s
  `Parts` comment for the canonical statement of this rule).
- **`Close` is idempotent and safe at any point** — before `Parts()` is
  ever ranged over (so a decided-not-to-consume caller doesn't leak the
  underlying HTTP body), mid-iteration, or after `Parts()` has already
  closed the stream itself on natural/abnormal end (making a
  caller's `Close()` a no-op). `Close` is *not* safe for concurrent use
  with an in-progress `Parts()` iteration from another goroutine — the
  contract assumes one consumer driving the stream, matching every
  `StreamResponse` implementation in this codebase (see
  [Telemetry § Single-consumer caveat](core/telemetry.md#single-consumer-caveat)
  for where this bites a wrapper).

## Replay-safe informational parts

`ToolCallEnd`, `ReasoningEnd`, `SourceEvent`, and `FinishPart` are, by
design, self-contained snapshots rather than deltas that must be applied
in sequence to reconstruct state — each carries a complete value
(`Call`/`Part`/`Source`, or `Reason`+`Usage`) rather than a fragment that
depends on prior parts to interpret. The proof this matters in practice:
`ai.SimulateStreamingMiddleware` turns a plain `Generate` response into a
*synthetic* stream by directly re-emitting these part types straight from
`Response.Content` — a `ReasoningDelta` + `ReasoningEnd{Part: rp}` per
reasoning part, a `ToolCallEnd{Call: tc}` per tool call, one `FinishPart`
built from `Response.FinishReason`/`Usage` — with no real streaming
transport underneath at all. That only works because those part types are
"replay-safe": constructing one from a complete `Response` and yielding it
once produces the exact same value a real incremental stream would have
produced, with no accumulated-state dependency on delta ordering. Only
`TextDelta`/`ToolCallDelta`/`ReasoningDelta` are true deltas — order-
dependent fragments a consumer accumulates — everything else in
`provider.StreamPart` is replay-safe by construction.

## How to add a provider

1. **Decide the shape.** OpenAI-chat-completions-compatible API → a
   preset over `internal/openaicompat` (see `providers/groq` for the
   shortest example). Gemini-`generateContent`-compatible → a preset over
   `internal/geminicompat`. Anything else → a full implementation against
   `provider.LanguageModel` directly (see `providers/mistral` or
   `providers/cohere` for a from-scratch reference; both are compact,
   fully own their wire format, and stay well under a small handful of
   files).
2. **Implement `provider.LanguageModel`** (`Generate`, `Stream`,
   `ModelID`, `ProviderName`, `Capabilities`) — and, if the vendor
   supports it, `provider.EmbeddingModel`/`EmbeddingModelWithOptions`,
   `ImageModel`, `SpeechModel`, `TranscriptionModel` (see `provider/model.go`,
   `provider/image.go`, `provider/speech.go`, `provider/transcription.go`).
3. **Follow the `StreamResponse` disciplines above** for `Stream`'s
   return value: single-use `Parts()`, wire-driven yields, exactly one
   `FinishPart` (or none, with `Err()` set), the truncation rule if your
   transport can end without a clean terminator, idempotent `Close`.
4. **Wire `ProviderOptions`/`ProviderMetadata`** per the
   [raw-wire-key convention](core/provider-options.md) — merge
   `Call.ProviderOptions[cfg.Name]` verbatim into the outgoing JSON (or
   form fields, for multipart requests), and populate
   `Response.ProviderMetadata[cfg.Name]`/`FinishPart.ProviderMetadata[cfg.Name]`
   with whatever response data doesn't have a typed home.
5. **Map errors through `ai.NewAPICallError`** (status code, URL, body,
   parsed message) so retryability (`err.Retryable`) is derived
   consistently — see [Errors and retries](core/errors-and-retries.md).
6. **Write the fixture server and call `providertest.Run`.** Every
   provider package's `*_test.go` builds an `httptest.Server` dispatching
   on the scenario keys in the [conformance suite table](#the-conformance-suite-providerprovidertest)
   above, constructs the provider's model pointed at that server, and
   calls `providertest.Run(t, providertest.Config{Model: model,
   ProviderName: "yourprovider"})`. This is the fastest way to catch a
   `StreamResponse` discipline violation before it ships. Add
   provider-specific tests (request shape, provider options merging,
   reasoning, provider metadata) alongside it.
7. **Add a package doc comment**, a `docs/providers/<name>.md` page, and a
   row in the README's provider/capability tables.
8. **Register it in the design spec's provider roadmap** if it fills in a
   previously-tracked-but-unimplemented provider entry.

## How to add a capability

The image/speech/transcription capabilities (`ImageModel`, `SpeechModel`,
`TranscriptionModel`) are the working template for adding a new one:

1. **Define the interface in `provider`** — a small, focused interface
   (mirroring `LanguageModel`'s shape: a `Call`-like request type, a
   `Result`-like response type, `ModelID`/`ProviderName`) in its own file
   (`provider/image.go`, `provider/speech.go`, `provider/transcription.go`
   are the precedents). Keep it minimal — don't fold new concerns into
   `LanguageModel` itself; capabilities are additive, optional interfaces
   a `providers/*` package implements only when the vendor supports them.
2. **Add the `ai`-layer entry point** — `ai.GenerateImage`/
   `GenerateSpeech`/`Transcribe` are the pattern: an `Opts` struct
   (`Model`, capability-specific fields, `MaxRetries`, `ProviderOptions`),
   a `Result` struct, a function wrapping the model call in the shared
   retry logic (`internal/retry`), validating required fields up front
   (see `ErrPromptRequired`/`ErrTextRequired`/`ErrAudioRequired` for the
   pattern) before ever calling the model.
3. **Extend `ai.Registry`** with a resolver method (`ImageModel`,
   `SpeechModel`, `TranscriptionModel` on `Registry` are the precedent) if
   the capability should be resolvable by `"provider:model"` string.
4. **Implement it per provider** where the vendor's API supports it —
   this is opt-in; a provider with no image API simply doesn't implement
   `provider.ImageModel`, and `ai.Registry.ImageModel` returns an error
   for it rather than every provider needing a stub.
5. **No shared conformance suite is required** for non-`LanguageModel`
   capabilities today — `providertest` covers `LanguageModel` only, so
   write focused per-provider tests (see `providers/openai`'s image/speech/
   transcription tests for the pattern: fixture server + assertions on
   the typed result).
6. **Document it**: a `core/<capability>.md` guide, a row in each
   relevant `docs/providers/<name>.md` page, and an entry in the README's
   capability matrix.

## Source of truth

- [`provider/model.go`](../provider/model.go), [`provider/stream.go`](../provider/stream.go),
  [`provider/call.go`](../provider/call.go), [`provider/response.go`](../provider/response.go)
- [`provider/providertest/providertest.go`](../provider/providertest/providertest.go)
- [`internal/openaicompat/language_model.go`](../internal/openaicompat/language_model.go)
  (canonical `StreamResponse` discipline implementation, including the
  truncation-rule comment)
- [`internal/geminicompat/geminicompat.go`](../internal/geminicompat/geminicompat.go)
- [`ai/middleware.go`](../ai/middleware.go) (`SimulateStreamingMiddleware`,
  the replay-safety proof)
- [Design spec](superpowers/specs/2026-08-02-go-ai-sdk-design.md) — original
  rationale and the full decisions log

See also: [Migrating from the Vercel AI SDK](migrating-from-vercel-ai-sdk.md)
for how the public API maps to Vercel's; [Provider overview](providers/README.md)
for the shipped roster.
