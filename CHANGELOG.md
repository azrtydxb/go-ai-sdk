# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once it reaches 1.0.

## [Unreleased]

A hardening sweep across security (SSRF, injection, RNG), concurrency
(goroutine/connection leaks, races), and correctness (two HIGH bugs
corrupting real output), driven by three top-level audits (security,
concurrency, correctness) plus three provider/core/MCP sub-audits. No
public API removed; a handful of internal-only behavior changes are called
out under Changed.

### Security

- **SSRF guards on server-controlled media/result-URL fetches.**
  `internal/fetchmedia.Fetch` (used directly by BFL's async polling, and
  wrapped by `internal/fetchimage` for fal/Replicate/Luma's CDN-URL image
  responses) now rejects link-local/multicast addresses (covering the
  169.254.169.254 cloud-metadata endpoint), AWS's IPv6 IMDS address
  (`fd00:ec2::254`), and the unspecified address, both pre-connect and, via
  a dial-time-pinned `http.Transport.DialContext`, at the moment of the
  actual TCP dial — closing the DNS-rebind gap where a hostname could
  resolve safely on the pre-connect check and unsafely when the transport
  dials it for real. Response bodies are capped at 256 MiB by default.
  BFL's `x-key` API credential is attached only to poll URLs sharing the
  configured base URL's registrable domain (a stdlib-only two-label
  heuristic), never to an arbitrary server-returned URL. Deliberately
  narrow: generic private ranges and loopback are not blocked, since
  self-hosted CDNs on private networks are a legitimate deployment. See
  [Media § Server-returned result-URL fetches](docs/core/media.md#server-returned-result-url-fetches-ssrf-hardening).
- **SSE and MCP response-body caps.** `internal/sse.MaxEventBytes` (32 MiB)
  caps a single accumulated SSE event's `data:` payload — an unbounded or
  stalled event stream can no longer grow a single event without limit.
  The MCP Streamable HTTP transport caps a non-streaming success body at
  16 MiB and an error/`202 Accepted` body at 64 KiB. See
  [Errors and retries § Timeouts](docs/core/errors-and-retries.md#timeouts-set-a-ctx-deadline)
  and [MCP § Response body caps](docs/mcp.md#response-body-caps-http-transport).
- **Multipart CRLF/quote injection guard.** `internal/multipartutil.ValidField`
  rejects control characters and unescaped quotes in any string written
  into a multipart header (filenames, field names, form values reaching a
  `Content-Disposition`), applied across every multipart-uploading call
  site: openai/anthropic files and skills, and the revai/gladia/elevenlabs/
  openaicompat transcription and translation uploads. Prompt/body field
  *values* (not header-reaching strings) are correctly left unguarded.
- **gauth signing uses `crypto/rand`.** Google service-account JWT signing
  (`rsa.SignPKCS1v15`) now passes `rand.Reader` (PKCS#1 v1.5 blinding)
  instead of `nil`, closing a timing-side-channel exposure in the Vertex AI
  auth path.
- **MCP HTTP retry is now conservative (no more double-execution risk).**
  `WithHTTPRetry` only retries HTTP 429/503 responses and a narrow
  allowlist of *pre-delivery* connection errors (connection-refused, DNS
  failure, dial-phase errors) — cases that prove the request never reached
  the server. A generic post-dial `client.Do` error (e.g. connection reset
  while reading the response) is no longer retried, since the server may
  already have processed the request; retrying it could execute a
  non-idempotent `tools/call` twice. See
  [MCP § Token-provider auth and retries](docs/mcp.md#token-provider-auth-and-retries-http-transport).
- **MCP server-request dispatch is bounded and drained.** At most 8
  server-initiated requests (elicitation today) are dispatched
  concurrently; a 9th while all slots are busy gets an immediate `-32603`
  "server busy" reply instead of queuing without limit. `Client.Close`
  waits (up to a 2s grace period) for in-flight dispatch goroutines to
  finish, so a handler that ignores `ctx` cancellation can't leak past
  `Close`. See [MCP § Dispatch is bounded](docs/mcp.md#elicitation).

### Fixed

- **HIGH: no-argument tool calls always failed.** `Tool.Execute` now
  normalizes an empty/nil/whitespace-only arguments payload to `{}` before
  decoding; previously this hit `io.EOF` and every genuinely no-input tool
  call was rejected as `*ai.InvalidToolArgumentsError`. See
  [Tools § No-argument tool calls](docs/core/tools.md#no-argument-tool-calls).
- **HIGH: Gemini "thought" text leaking into the visible answer**
  (google/vertex, both streaming and non-streaming) — thought-marked parts
  are now split into `ReasoningPart`/`ReasoningDelta` instead of being
  concatenated into the regular text output; `ThoughtSignature` is plumbed
  through on the non-streaming path.
- **Bedrock: a caller-supplied `Content-Type` header clobbered the SigV4
  signature**, producing a `403 SignatureDoesNotMatch`. `Content-Type` is
  now excluded from the post-signing header override, matching the
  provider-owned value (`application/json`) that was actually signed.
- **Middleware dropped `provider.ProviderMetadata`.**
  `ExtractReasoningMiddleware` and `SimulateStreamingMiddleware` now
  preserve the wrapped model's `ProviderMetadata` instead of discarding it.
- **`StreamText`'s `Timeout.Total` leaked on immediate suspend.** A run
  that suspends on its very first step (a pending tool approval) no longer
  leaves the `Total` timeout's derived context/timer running past the
  point the decision (finish/timeout/abort) was actually cached.
- **`RealtimeSession` WebSocket connection leak** on close — a `defer`
  now closes the underlying connection unconditionally; regression tests
  observe a real `EOF` in all three WebSocket-based provider packages.
- **MCP stdio transport: ctx-blind write could wedge the client.** `Send`
  is now ctx-aware (a watcher goroutine plus `SetWriteDeadline`), abandoning
  only on a genuinely partial write rather than blocking forever on a
  stalled child process. A transport-level error no longer leaves the
  client in a zombie state (`closeWith` now routes through
  `transport.Close`), and a hung response body no longer survives `Close`
  (tracked reads via `readTrackedBody`, including the per-session `202`
  path, are interrupted).
- **MCP: calling a method after the client has already died** now returns
  a clear, immediate error instead of hanging or panicking.
- **MCP `paginate` infinite-loop guard** — a server that echoes back the
  same (or a cyclical) `nextCursor` forever no longer causes an unbounded
  `ListTools`/`ListResources`/etc. loop.
- **Cohere embed response validated against the input count** — a
  mismatched number of returned embeddings is now a clear error instead of
  a silent misalignment between input and output.
- **Code Mode (`codemode.Tool`) output-budget and empty-code fixes.**
  `MaxOutputBytes` now applies to the *combined* `Output` + `Logs`, so log
  volume can't blow past the configured budget on its own; empty or
  whitespace-only generated code is now rejected with
  `*ai.InvalidToolArgumentsError` before the sandbox is ever invoked.
- **`aitest` mock providers are now concurrency-safe** — a per-mock
  `sync.Mutex` guards every record-append and response-index computation,
  fixing a data race under concurrent test use; `Recorded*` snapshot
  accessors were added.
- **Retry timer/backoff footguns** — the retry loop now calls
  `timer.Stop()` explicitly instead of `defer`-ing it inside the retry
  loop (which could accumulate live timers across iterations), and guards
  against a non-positive configured `BaseDelay`.
- **`internal/schema`'s wire-shape mismatches for `[]byte` and
  `time.Time`** — both a tool's derived argument schema and
  `GenerateObject`/`StreamObject`'s derived output schema now describe
  `[]byte` as a base64-encoded string and `time.Time` as an RFC 3339
  `date-time` string, matching what `encoding/json` actually produces,
  instead of describing them by their underlying Go field structure. See
  [Tools § Schema derivation rules](docs/core/tools.md#schema-derivation-rules).
- **gauth token-endpoint errors are now retryable** — a non-2xx response
  from Google's OAuth2 token endpoint is returned as a typed
  `*gauth.TokenEndpointError` implementing `IsRetryable() bool`, so a
  transient token-endpoint failure during Vertex AI auth is retried the
  same way a transient API call would be.
- **MCP elicitation reachability.** The client now offers protocol version
  `2025-06-18` (accepting `2025-06-18` or `2025-03-26` back) instead of
  pinning to `2025-03-26` alone, making `elicitation/create` (a
  `2025-06-18` feature) actually reachable against a conforming server. A
  handler that returns an error is now reported to the server as a typed
  `-32603 Internal error` reply, distinguishable from a genuine
  `Action: "cancel"` user decision. See
  [MCP § Elicitation](docs/mcp.md#elicitation).
- Eight `nil`-`Model` middleware constructors (`ExtractReasoningMiddleware`,
  `SimulateStreamingMiddleware`, and others) now panic immediately at
  construction on a `nil` wrapped model, instead of surfacing a confusing
  nil-pointer panic later, mid-call.

### Changed

- **`mcp.WithHTTPRetry` is now conservative by default**, retrying only
  HTTP 429/503 and pre-delivery connection errors rather than a broader
  set of transient-looking failures — see the Security entry above. This
  narrows what gets automatically retried; callers that relied on broader
  coverage should wrap their own idempotency-aware retry logic around
  `WithHTTPClientOpt` instead (documented on the option itself).
- **Middleware constructors taking a `nil` wrapped `Model` now panic at
  construction time** (see Fixed above) instead of deferring the failure
  to first use.

## [0.2.0] — 2026-08-03

Waves 9, 10, 11, 12, 13, and 14 of the [AI SDK 6 parity roadmap](docs/superpowers/plans/2026-08-03-v6-parity-roadmap.md) —
the closing release of the parity program. Wave 9: v5 leftovers plus quick
AI SDK 6 wins. Wave 10: output modes on `GenerateText`, reranking, a
unified reasoning option, and the full lifecycle-callback event set. Wave
11: tool-execution approvals with a resumable pending-approval flow,
`RuntimeContext`, an `agent.Agent` package, and Code Mode
(`codemode.Tool`/`Sandbox`). Wave 12: video generation, a stdlib-only
WebSocket client, streaming transcription, audio translation, a minimal
OpenAI realtime voice session, and file/skill upload. Wave 13: MCP
resources/prompts/completions/elicitation and token-provider auth with
transient retry on the HTTP transport, plus 14 new providers (Moonshot,
Qwen, MiniMax, DeepInfra, Hugging Face, Baseten, LM Studio, NVIDIA NIM,
Voyage, Mixedbread, Cartesia, Prodia, Black Forest Labs, Vercel AI
Gateway), bringing the provider total to 39. Wave 14: a real OpenTelemetry
bridge (`contrib/otel`, a separate nested Go module), tool `strict` mode
and `inputExamples` (plus `AddToolInputExamplesMiddleware`), per-tool
input-streaming lifecycle hooks, and structured `Timeout`
(Total/Step/Chunk) with `*ai.TimeoutError` — closing out the final
gap-audit's four remaining items. **`go-ai-sdk` now has full parity with
the AI SDK 6 core** — see
[Migrating from the Vercel AI SDK § AI SDK 6 delta](docs/migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta)
for the feature-by-feature status and the
[v6 parity final audit](docs/superpowers/specs/2026-08-03-v6-parity-final-audit.md)
for the closing record.

### Added

**Wave 14**

- `contrib/otel`: a real OpenTelemetry bridge — `github.com/azrtydxb/go-ai-sdk/contrib/otel`,
  a **separate Go module** (its own `go.mod`/`go.sum`, `replace
  github.com/azrtydxb/go-ai-sdk => ../..` for local development only) so
  the root module stays zero-dependency. `otelbridge.New(...Option)`
  returns a `*Bridge` implementing `ai.Telemetry`: starts a
  `trace.SpanKindClient` span per call (`"chat " + ModelID`, both
  `SpanInfo.Operation` values mapping to GenAI's `"chat"` operation),
  keyed by `SpanInfo.CorrelationID`, parented under any span already in
  the call's `ctx`. Emits GenAI semantic-convention attributes
  (`gen_ai.operation.name`, `gen_ai.system`, `gen_ai.request.model`,
  `gen_ai.usage.input_tokens`/`.output_tokens`,
  `gen_ai.response.finish_reasons`) as plain string keys (not coupled to a
  versioned `semconv` package), plus `codes.Ok`/`codes.Error` status and
  `RecordError` on failure. `WithTracer`/`WithSpanNamePrefix` configure it.
  Tested with an in-memory `tracetest.SpanRecorder` — no network/collector
  required; verify with `cd contrib/otel && go test -race ./...` (a
  separate module, invisible to the root `go test ./...`). See
  [Telemetry § The contrib/otel bridge](docs/core/telemetry.md#the-contribotel-bridge)
  and [`contrib/otel/README.md`](contrib/otel/README.md).
- Tool `strict` mode and `inputExamples`: `ai.WithToolStrict()` and
  `ai.WithToolInputExamples[Args](examples ...Args)`, two new `NewTool`
  `ToolOption`s setting `Tool.Strict()`/`Tool.InputExamples()`
  (`provider.ToolDef.Strict`/`.InputExamples` downstream). `Strict` is
  honored as `"strict":true` by openaicompat-based providers, ignored
  (no wire param, no error) by anthropic, geminicompat, bedrock, cohere,
  mistral. `InputExamples` is sent natively only by anthropic
  (`input_examples`); the anthropic provider automatically sends the
  required `anthropic-beta: advanced-tool-use-2025-11-20` header whenever
  a tool carries `InputExamples`, without the caller needing to set it.
  Every other provider needs
  `ai.AddToolInputExamplesMiddleware(model)`, which folds each tool's
  examples into its `Description` as text (`"\n\nExample inputs:\n"` plus
  one compact-JSON example per line) and clears `InputExamples`, so a
  native-support provider never double-counts them — idempotent per call,
  the caller's original `Tools`/`ToolDef` values are never mutated. See
  [Tools § Strict mode and input examples](docs/core/tools.md#strict-mode-and-input-examples)
  and [§ AddToolInputExamplesMiddleware](docs/core/tools.md#addtoolinputexamplesmiddleware).
- Per-tool input-streaming lifecycle hooks:
  `ai.WithToolInputCallbacks(ai.ToolInputCallbacks{OnInputStart,
  OnInputDelta, OnInputAvailable})`, mirroring the Vercel AI SDK v6's
  `onInputStart`/`onInputDelta`/`onInputAvailable`. `StreamText` fires
  `OnInputStart` once per `toolCallID` on its first argument delta,
  `OnInputDelta` on every delta thereafter (raw args-JSON text fragment),
  and `OnInputAvailable` once arguments are fully assembled, immediately
  before that call executes; `GenerateText` (no deltas) fires only
  `OnInputAvailable`. A `RepairToolCall` retry re-fires `OnInputAvailable`
  for the repaired call. None of the three fire for the `Output`
  tool-mode fallback's synthetic forced call, since that call is decoded
  directly and never routed through `Tool.Execute`. See
  [Tools § Per-tool input streaming hooks](docs/core/tools.md#per-tool-input-streaming-hooks).
- `GenerateTextOpts.Timeout{Total, Step, Chunk time.Duration}` /
  `*ai.TimeoutError{Dimension, Limit}`: structured timeout bounds finer
  than a single `context.Context` deadline. `Total` bounds the whole run
  (a `context.WithTimeout` derived at entry, layered on the caller's own
  `ctx`); `Step` bounds each individual step's model call; `Chunk`
  (`StreamText` only) bounds the max gap between yielded
  `provider.StreamPart`s, implemented as a timer reset on every part.
  Whichever bound fires first ends the run with a `*ai.TimeoutError`
  (`Dimension: "total"|"step"|"chunk"`) via the returned error/`OnError` —
  detected via a distinct `context.WithTimeoutCause` sentinel per
  dimension, so this is always distinguishable from the caller's own
  `ctx` being canceled or reaching its own deadline, which continues to
  surface exactly as before (the plain ctx error / `OnAbort` in
  `StreamText`, never a `*ai.TimeoutError`) — a shorter caller-supplied
  `ctx` always wins over a much larger `Total`. See
  [Generating text § Timeout](docs/core/generating-text.md#timeout-total-step-and-chunk)
  and [Streaming § Timeout: the Chunk dimension](docs/core/streaming.md#timeout-the-chunk-dimension).

**Wave 13**

- `mcp`: resources, resource templates, and prompts —
  `Client.ListResources`/`ListResourceTemplates`/`ReadResource`,
  `ListPrompts`/`GetPrompt`, each gated on the server's advertised
  `"resources"`/`"prompts"` capability (a `*mcp.CapabilityError` is
  returned, with no request sent, if the server didn't declare it).
  `Resource`/`ResourceTemplate`/`ResourceContents`/`Prompt`/
  `PromptArgument`/`PromptMessage`/`PromptPart` are the new domain types;
  an embedded resource inside a prompt message decodes through the same
  `ResourceContents` shape `ReadResource` uses. A prompt message's
  `content` field is accepted as either a single object or an array,
  always flattened into `[]PromptPart`; unrecognized content-part types
  are preserved (not errored), per MCP's forward-compatible convention.
  `ListTools`'s pagination loop was generalized into a shared `paginate`
  helper reused by all four new list methods, with no behavior change to
  `ListTools` itself. See [MCP § Resources and resource templates](docs/mcp.md#resources-and-resource-templates)
  and [§ Prompts](docs/mcp.md#prompts).
- `mcp`: argument completions and server-initiated elicitation —
  `Client.Complete` (`completion/complete`, gated on the `"completions"`
  capability); `Client.SetElicitationHandler`/`ElicitationHandler`,
  handling server-initiated `elicitation/create` requests via a new
  `recvLoop` discrimination (`id`+no `method` → response; `id`+`method` →
  server-initiated request, dispatched to its own goroutine; no `id` →
  notification, dropped as before). A `nil` handler auto-declines and
  omits the `"elicitation"` capability from `Initialize`; a handler error
  is reported to the server as `Action: "cancel"`. **Elicitation only
  works over the stdio transport** — Streamable HTTP has no server→client
  channel to receive a server-initiated request on, so this is a real,
  documented gap rather than a hypothetical one. See
  [MCP § Completions](docs/mcp.md#completions) and
  [§ Elicitation](docs/mcp.md#elicitation).
- `mcp`: token-provider auth and transient retry on the Streamable HTTP
  transport — `NewStreamableHTTPTransportWithOptions`, `TokenProvider`/
  `TokenProviderFunc`, `WithTokenProvider`, `WithAuthHeader`,
  `WithHTTPRetry(maxRetries)` (capped exponential backoff, `Retry-After`
  honored, retries only HTTP 429/503 and connection errors, never once
  response bytes have started being consumed, ctx-aware backoff waits),
  `WithHTTPClientOpt`. The `TokenProvider` is re-invoked fresh on every
  request and every retry attempt; the transport does not retry 401s
  itself. `NewStreamableHTTPTransport` is unchanged (now a thin wrapper).
  See [MCP § Token-provider auth and retries](docs/mcp.md#token-provider-auth-and-retries-http-transport).
- Eight new `internal/openaicompat` preset providers: `providers/moonshot`
  (`MOONSHOT_API_KEY`), `providers/qwen` (`DASHSCOPE_API_KEY`,
  `max_tokens` field name, embeddings), `providers/minimax`
  (`MINIMAX_API_KEY`, `max_tokens` field name), `providers/deepinfra`
  (`DEEPINFRA_API_KEY`, `/v1/openai` base path, embeddings with a 1024
  batch size), `providers/huggingface` (`HF_TOKEN`, `NativeJSON: false`
  since the router fans out to heterogeneous backends, `max_tokens` field
  name), `providers/baseten` (`BASETEN_API_KEY`, embeddings),
  `providers/lmstudio` (`LMSTUDIO_API_KEY`, local-first: no key required,
  defaults to `http://localhost:1234/v1`, embeddings), and
  `providers/nvidia` (`NVIDIA_API_KEY`, embeddings). All eight route their
  quirks entirely through existing `openaicompat.Config` fields — no new
  `Config` field was needed. See [Provider overview](docs/providers/README.md).
- `providers/gateway`: Vercel AI Gateway, also an `openaicompat` preset
  (`AI_GATEWAY_API_KEY`, `NativeJSON: false` since routing slugs resolve
  to heterogeneous upstream models, embeddings with a batch size of 1).
  Routing slugs (e.g. `"openai/gpt-4o"`) pass through verbatim as the wire
  `model` field, including through `ai.Registry` — `splitID` cuts on the
  first colon only, so a slug's internal slashes survive
  `"gateway:openai/gpt-4o"`-style registry lookups intact. OIDC
  (`VERCEL_OIDC_TOKEN`) is out of scope; only the API-key flow is
  supported. See [Vercel AI Gateway](docs/providers/gateway.md).
- `providers/voyage`: `provider.EmbeddingModel` (also
  `EmbeddingModelWithOptions`, `MaxBatchSize() == 128`) and
  `provider.RerankingModel` against Voyage AI's embeddings and rerank
  APIs (`VOYAGE_API_KEY`). Embeddings are placed at their response
  `index` rather than appended in response order; both embed and rerank
  report token-based `Usage.TotalTokens`. See
  [Embeddings § Reranking](docs/core/embeddings.md#reranking) and
  [Voyage](docs/providers/voyage.md).
- `providers/mixedbread`: `provider.RerankingModel` against Mixedbread
  AI's rerank API (`MXBAI_API_KEY`) — `documents` maps to Mixedbread's
  `input` wire field (not `documents`) and the response's per-result
  `score` field (not `relevance_score`), both deliberately different
  field names from Cohere/Voyage's rerank shape; `Usage` is left zero (no
  token-usage field in the documented response). See
  [Mixedbread](docs/providers/mixedbread.md).
- `providers/cartesia`: `provider.SpeechModel` against Cartesia's
  text-to-speech API (`CARTESIA_API_KEY` + `Cartesia-Version` header);
  `Voice` is required (a hard error, unlike every other speech provider's
  substituted default); `OutputFormat` maps to a nested
  `output_format.container`/`.encoding` object at a fixed 44100 sample
  rate. See [Cartesia](docs/providers/cartesia.md).
- `providers/prodia`: `provider.ImageModel` against Prodia's v2
  synchronous `/job` endpoint (`PRODIA_API_KEY`); the response body IS
  the generated image bytes (no JSON envelope, so `ImageResponse.Raw` is
  nil); `ProviderOptions` merge into the nested `config` object, not the
  top level. See [Prodia](docs/providers/prodia.md).
- `providers/bfl`: `provider.ImageModel` against Black Forest Labs'
  asynchronous image API (`BFL_API_KEY`, `x-key` header, not
  `Authorization`) — a generation is created, then polled at the
  **absolute** `polling_url` the create response returns (never a path
  this SDK builds itself) until a `"Ready"`/failure terminal state.
  See [Black Forest Labs](docs/providers/bfl.md).
- 14 new [provider pages](docs/providers/) and a wave-13 documentation
  pass across [MCP](docs/mcp.md), [Provider overview](docs/providers/README.md)
  (25→39 rows across the capability matrix, construction-at-a-glance
  table, and provider-page list), [Embeddings § Reranking](docs/core/embeddings.md#reranking),
  [Media](docs/core/media.md), [Getting started](docs/getting-started.md)
  (env var table), [`README.md`](README.md), and
  [Migrating from the Vercel AI SDK](docs/migrating-from-vercel-ai-sdk.md)
  (MCP scope and provider-fleet rows now **Shipped**; the former
  "MCP is tools-only" section retitled to
  [MCP scope](docs/migrating-from-vercel-ai-sdk.md#mcp-scope) to reflect
  the expanded surface).

**Wave 12**

- `ai.GenerateVideo`/`provider.VideoModel`/`Registry.VideoModel`: video
  generation, mirroring `ai.GenerateImage`'s shape exactly (`Prompt`
  required, `AspectRatio`/`Resolution`/`DurationSec` optional and
  provider-defined when unset, standard retry wrapper). Luma (Dream
  Machine, `POST /dream-machine/v1/generations` then poll until
  `"completed"`/`"failed"`, `DurationSec` mapped to a `"5s"`-style
  duration string), fal (synchronous `POST {base}/{modelID}`; only
  `Prompt`/`AspectRatio` are first-class fields, everything else is
  `ProviderOptions`-only), and Replicate (synchronous `Prefer: wait`,
  `Prompt`/`AspectRatio` nested under `input`) implement it.
  `provider.GeneratedVideo` additionally carries `URL` (the provider's
  source URL, which may expire) alongside `Data`/`MediaType`. See
  [Media § GenerateVideo](docs/core/media.md#generatevideo).
- `internal/websocket`: a dependency-free, client-only RFC 6455 WebSocket
  implementation (`Dial`, `Conn.Read`/`WriteText`/`WriteBinary`/`Close`,
  ctx-aware cancellation, automatic ping/pong and close-frame handling,
  `MaxMessageBytes` enforcement before any oversized payload is read) —
  the transport underlying every streaming/realtime feature below.
  `internal/websocket/websockettest` ships exported server-side test
  helpers (`Accept`/`Upgrade`/`ReadMessage`/`WriteMessage`/`WriteClose`)
  reused by the providers' fixture tests. Internal — no public API of its
  own; see [Architecture](docs/architecture.md).
- `ai.StreamTranscribe`/`provider.StreamingTranscriptionModel`/
  `TranscriptionStream`/`TranscriptEvent`: live, bidirectional
  transcription — one goroutine can `Send` audio while another ranges
  over `Events()`. No retry wrapper (a live connection can't be
  transparently retried). `Events()` is single-use; `Err()` is `nil` on a
  clean end (provider close or caller `Close()`); `Send`/`CloseSend`
  after `Close`, or `Send` after `CloseSend`, return descriptive errors;
  an abandoned `Events()` range followed by `Close()` still reclaims the
  reader goroutine. Deepgram (`wss://.../v1/listen` live, query-param
  encoding/sample-rate mapping reusing the REST path's convention, empty-
  transcript `Results` messages skipped even when `is_final:true`) and
  OpenAI (`wss://.../realtime?intent=transcription`,
  `transcription_session.update` on open, `error` events terminal)
  implement it. See
  [Media § StreamTranscribe](docs/core/media.md#streamtranscribe).
- `ai.Translate`/`provider.TranslationModel`: audio translation into
  English text regardless of source language, same retry-wrapped shape as
  `ai.Transcribe`. OpenAI only (`internal/openaicompat.NewTranslationModel`,
  multipart `POST /audio/translations`, `response_format` always
  `verbose_json`). Not wired into `ai.Registry`. `StreamTranslate` was
  **not** shipped this wave — none of the targeted providers expose a
  live/streaming audio-translation API. See
  [Media § Translate](docs/core/media.md#translate).
- `(*openai.Provider).RealtimeSession`/`RealtimeConfig`/`RealtimeEvent`: a
  minimal realtime voice/text session over OpenAI's Realtime API —
  `SendAudio`/`CommitAudio`/`SendText`/`CreateResponse`, single-use
  `Events()` (both old and new audio/text delta event names mapped),
  `Raw` always set on every event. Unlike `StreamTranscribe`'s streams, a
  server `error` event does **not** end the session — it surfaces as an
  ordinary event, and only a socket failure, ctx cancellation, or
  `Close()` ends iteration. OpenAI-only: no generic
  `provider.RealtimeModel` interface, not wired into `ai.Registry`. See
  [Media § Realtime voice session](docs/core/media.md#realtime-voice-session-openai-only).
- `provider.FilePart` gains `FileID`/`URL` fields (exactly one of
  `Data`/`FileID`/`URL` must be set): OpenAI and other `openaicompat`
  providers (`FileID` only, `{"file":{"file_id":...}}`), Anthropic
  (`FileID` and `URL`, both as `"document"` block source variants),
  Google/Vertex AI (`geminicompat`, `URL` only, a `fileData` part).
  Bedrock accepts neither (Converse's document block has no
  file-reference primitive). See
  [Media § FileID and URL variants](docs/core/media.md#fileid-and-url-variants).
- `ai.UploadFile`/`ai.DeleteFile`/`provider.FileStore`: upload once,
  reference the returned ID from a later prompt via the new
  `FilePart.FileID`. OpenAI (`POST`/`DELETE /files`) and Anthropic
  (`POST`/`DELETE /v1/files`, `anthropic-beta: files-api-2025-04-14` —
  isolated to `files.go`, never leaking onto `/v1/messages`) implement
  `FileStore`. Not wired into `ai.Registry`. See
  [Media § Files & skills](docs/core/media.md#files--skills).
- `(*anthropic.Provider).UploadSkill`/`.DeleteSkill`: Anthropic's Skills
  API (`uploadSkill` in Vercel's terms) — a distinct, **Anthropic-only**
  capability with no generic `provider` interface, unlike Files.
  Multipart `POST /v1/skills` (file part `files[]`, field
  `display_name`), `anthropic-beta: skills-2025-10-02`. See
  [Media § Files & skills](docs/core/media.md#files--skills).

**Wave 11**

- `ai.RequireApproval(tool, when ...func(ctx, args) bool)` /
  `ai.ApprovalRequirer`: tool-execution approvals. A wrapped tool's calls
  require a decision before executing — resolved from
  `GenerateTextOpts.Approvals` (checked first, matched by `ToolCallID`),
  then `.ApproveToolCall` (called inline), else left pending. A pending
  call suspends its **entire** batch atomically — no call in that batch
  executes, even ones needing no approval or already decided.
  `GenerateTextResult.PendingApprovals` (and `TextStream.PendingApprovals()`
  for `StreamText`) reports the undecided calls; suspension is not an
  error (`OnFinish` still fires, `FinishReason` is `tool-calls`). A denied
  call is never executed; `*ai.ToolApprovalDeniedError` is recorded on its
  `ToolResultRecord.Err` and sent to the model as an `IsError` tool result.
  Resend the suspended `Messages` with `Approvals` set to resume. Does not
  apply to `Output`'s tool-mode fallback synthetic call. See
  [Tools § Approvals for tool execution](docs/core/tools.md#approvals-for-tool-execution).
- Resume-from-Messages as a standalone loop-entry capability: `Messages`
  ending in an unanswered assistant tool-call batch is now detected and
  run through the tool loop (including approval rules) before any model
  call, in both `GenerateText` and `StreamText` — independent of whether
  approvals are involved. See
  [Generating text § Resume-from-Messages is its own capability](docs/core/generating-text.md#resume-from-messages-is-its-own-capability).
- `GenerateTextOpts.RuntimeContext` (`ai.RuntimeContext`, a
  `map[string]any`) / `ai.RuntimeContextFrom(ctx)`: an arbitrary
  application-value bag installed once per run and readable inside
  `Tool.Execute`, `ApprovalRequirer.ApprovalRequired`, and
  `ApproveToolCall`. See [Tools § RuntimeContext](docs/core/tools.md#runtimecontext).
- `package agent`: `agent.Agent` (`Model`, `Instructions`, `Tools`,
  `MaxSteps` — defaulting to 8, not `ai.GenerateTextOpts`'s 1/16 —
  `StopWhen`, `Output`, `RuntimeContext`, `ApproveToolCall`,
  `PrepareOpts`), `agent.RunOpts` (`Prompt`/`Messages`/`Approvals`), and
  `Generate`/`Stream`, which assemble a `GenerateTextOpts` and delegate
  entirely to `ai.GenerateText`/`ai.StreamText` — no loop logic of its own.
  `agent.AsTool(a, name, description)` exposes an `Agent` as an `ai.Tool`
  taking `{"task": string}`, returning the sub-agent's decoded `Output`
  (else `Text`), with sub-agent errors wrapped in
  `*ai.ToolExecutionError`. An unset sub-agent `RuntimeContext` inherits
  the parent's (an explicitly empty `ai.RuntimeContext{}` isolates it
  instead). The AI SDK's `ToolLoopAgent` is named plainly `Agent` here.
  See [Agents](docs/core/agents.md).
- `package codemode`: `codemode.Tool(sandbox, tools, opts)` wraps a set of
  `ai.Tool`s into a single `run_code` tool; `codemode.Sandbox` is the
  interface the caller implements against their own runtime — the SDK
  ships no bundled code runtime, and sandboxing/security is entirely the
  implementer's responsibility. `codemode.APIDoc` renders each tool's
  schema as a one-level-nested parameter listing for the model
  (alphabetically sorted properties, `object`-typed fields expanded one
  level then collapsed, arrays recursing through the same rule,
  properties-less schemas rendering `(args: object)`). `Tool` panics at
  construction on a duplicate tool name. `Result.Output` is truncated
  (default 16384 bytes, rune-safe, `"\n[truncated]"` suffix) and
  `Result.Logs` appended as `"\nlog: "` lines; sandbox errors and unknown
  tool-name errors propagate unwrapped (the `ai` tool loop's usual
  `*ai.ToolExecutionError` wrapping applies exactly once, one layer up).
  See [Code Mode](docs/core/code-mode.md).
- `GenerateTextOpts.SchemaDescription`: describes the expected `Output`
  schema; used as the injected output tool's `Description` in the
  tool-mode fallback (no effect in native-JSON mode). See
  [Generating text § SchemaDescription](docs/core/generating-text.md#schemadescription).
- `provider.ResolveBudgetTokens(cfg *ReasoningConfig) (int, bool)`: the
  shared `BudgetTokens`-else-`EffortBudgetTokens(Effort)` resolution used
  by Anthropic, Google/Vertex AI (geminicompat), and Bedrock, replacing
  three byte-identical private copies — an internal consolidation with no
  behavior change, exported for reuse. See
  [Reasoning § EffortBudgetTokens](docs/core/reasoning.md#effortbudgettokens-the-effort--token-budget-table).

**Wave 10**

- `GenerateTextOpts.Output`: structured-output modes on `GenerateText` —
  `ai.OutputObject[T]`, `ai.OutputArray[T]`, `ai.OutputChoice`, `ai.OutputJSON`,
  extracted via `ai.OutputAs[T]`. Uses the same native-JSON/forced-tool-call
  fallback as `GenerateObject`; the tool-mode fallback requires `Tools` to be
  empty (`ai.ErrOutputRequiresJSONOrNoTools` otherwise) and decodes the
  forced tool call's arguments directly without executing it as a real tool
  call. `StreamText` returns `ai.ErrOutputWithStreamText` immediately if
  `Output` is set — partial-output streaming is deferred to a later wave.
  See [Generating text § Output modes](docs/core/generating-text.md#output-modes).
- `ai.Rerank` / `provider.RerankingModel` / `Registry.RerankingModel`:
  document reranking — rank a set of documents by relevance to a query.
  Cohere implements it (`Provider.RerankingModel(id)`, `POST /rerank`);
  Cohere bills reranking in search units, not tokens, so
  `RerankResponse.Usage` is left zero and the raw response body (including
  billing metadata) is preserved in `RerankResponse.Raw`. See
  [Embeddings § Reranking](docs/core/embeddings.md#reranking).
- `GenerateTextOpts.Reasoning` / `provider.ReasoningConfig{Effort, BudgetTokens}`:
  a unified reasoning/thinking request option, mapped to each provider's
  native knob — `reasoning_effort` (openaicompat-based providers), a token
  budget resolved via the new exported `provider.EffortBudgetTokens` table
  (`minimal`→1024, `low`→4096, `medium`→8192, `high`→16384) for Anthropic
  (`thinking`), Google/Vertex AI (`thinkingConfig`), and Bedrock
  (`additionalModelRequestFields.thinking`); a no-op for Cohere and Mistral.
  `ProviderOptions` still merges last and wins on a wire-key collision, the
  same as every other `Call` field. `DefaultSettingsMiddleware` fills
  `Reasoning` from its defaults when a per-call value is unset. See
  [Reasoning § Requesting reasoning](docs/core/reasoning.md#requesting-reasoning-generatetextoptsreasoning).
- Full lifecycle-callback event set: `GenerateTextOpts.OnModelCallStart`/
  `OnModelCallEnd` (once per step, around the retried model call);
  `OnToolExecutionStart`/`OnToolExecutionEnd` (once per tool call record,
  collapsing a `RepairToolCall` retry into a single pair); `EmbedOpts`/
  `EmbedManyOpts.OnEmbedStart`/`OnEmbedEnd` (once per call, or once per
  batch for `EmbedMany`); `RerankOpts.OnRerankStart`/`OnRerankEnd`. Every
  End-callback's error is the same error the caller's function returns
  (retry exhaustion already translated to `*ai.RetryError`, never the raw
  retry-internal error) — see the new shared `translateRetryErr` helper in
  `ai/errors.go`. `OnModelCallEnd` does not fire on `StreamText`'s abort
  path (consumer abandonment or ctx cancellation); `OnAbort` covers that
  case instead. See
  [Generating text § Lifecycle callbacks](docs/core/generating-text.md#lifecycle-callbacks-model-call-and-tool-execution).

**Wave 9**

- `GenerateTextOpts`/`provider.Call`: first-class `TopK`, `PresencePenalty`,
  `FrequencyPenalty`, and `Seed` sampling settings, threaded through to
  every language-model request path (per-provider support and wire-name
  mapping documented on each field), plus `Headers` for per-call extra HTTP
  headers (applied after — and never overriding — each provider's own
  auth header(s); SigV4-signed for Bedrock when the key starts with
  `x-amz-`).
- `ai.HasToolCall(names ...string)` and `ai.LoopFinished()`: two more
  ready-made `StopWhen` helpers alongside `ai.StepCountIs`.
- `GenerateTextOpts.OnAbort`: fires exactly once per `TextStream` when the
  consumer abandons `Parts()` iteration early or the call context is
  canceled mid-stream — mutually exclusive with `OnFinish`/`OnError` for
  the same event.
- `ai.ToolResultContent`: multi-modal tool results — a `Tool.Execute` can
  return text plus one or more `provider.GeneratedImage`s. Serialized
  natively by anthropic and bedrock; text-projected (images dropped) by
  openaicompat-based providers, geminicompat, cohere, and mistral.
- `ai.ExtractJSONMiddleware`: strips markdown code fences (` ```json `)
  from a model's text output, both `Generate` and incrementally in
  `Stream`, reusing `GenerateObject`'s fence-stripping rule.
- `ai.WrapImageModel`: the `provider.ImageModel` counterpart to
  `ai.WrapModel` — a one-line naming hook for image-model middleware.
- AssemblyAI, Gladia, and Rev.ai `provider.TranscriptionModel`
  implementations (`providers/assemblyai`, `providers/gladia`,
  `providers/revai`), each an asynchronous upload/create-then-poll flow
  with `WithPollInterval`-controlled, ctx-aware polling — bringing the
  provider total to 25.

### Changed

**Wave 14 — BREAKING**

- **`ai.Telemetry.OnSpanStart` signature change:** now
  `OnSpanStart(ctx context.Context, info SpanInfo)` (previously
  `OnSpanStart(info SpanInfo)`). `ai.SpanInfo` also gains a new
  `CorrelationID string` field, populated identically on the
  `OnSpanStart`/`OnSpanEnd` (or stream-end) pair for one call — a
  process-wide monotonic atomic counter, not time-based, so it never
  collides under concurrent or fast-successive calls (unlike keying a
  span map by `StartTime`). **This is the only breaking change in the
  entire v6 parity program (waves 9–14).** It's pre-1.0, and the only
  known implementer of `Telemetry` in this repo before this wave was the
  docs' own OTel sketch (now replaced by the real
  [`contrib/otel`](contrib/otel/README.md) bridge, itself built against
  the new signature). Migration is a one-line signature update:
  ```go
  // Before (v0.1.x)
  func (t *myTelemetry) OnSpanStart(info ai.SpanInfo) { ... }
  // After (v0.2.0)
  func (t *myTelemetry) OnSpanStart(ctx context.Context, info ai.SpanInfo) { ... }
  ```
  `OnSpanEnd`'s signature is unchanged. See
  [Telemetry § Telemetry and SpanInfo](docs/core/telemetry.md#telemetry-and-spaninfo).

**Wave 9**

- `GenerateTextOpts.StopWhen` is now consulted after **every** completed
  step, not only steps that requested tool calls — this removes the
  previously-documented divergence from Vercel's `stopWhen`, which is
  consulted after every step too. A step with no tool calls still always
  ends the loop naturally regardless of what `StopWhen` returns for it.

### Fixed

**Wave 10**

- `ai.Rerank`: `OnRerankEnd` now receives the same translated `*ai.RetryError`
  that `Rerank` itself returns on retry exhaustion, instead of the raw
  retry-internal error (an asymmetry introduced when reranking first
  shipped, fixed before this wave's lifecycle-callback work was final).

**Wave 9**

- `providers/revai`: documented that `"unknown"`-type transcript elements
  (unintelligible speech) are intentionally omitted from both `Text` and
  `Segments`.
- `providers/gladia`: documented that the job-creation response's
  `result_url` is intentionally unused — this provider polls by job `id`
  instead.
- `providers/assemblyai`: a terminal `"error"` transcript status with an
  empty `error` field now includes the raw response body in the returned
  error, instead of reporting the failure with no detail at all.

## [0.1.0] — 2026-08-03

The public API described in the [design spec](docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md)
is implemented and tested end-to-end: the full core SDK, 22 providers, media
capabilities, and the parity features listed below. Providers are verified
against recorded/documented wire formats via fixture tests; none have been
smoke-tested against live APIs yet (see the
[live-testing status](docs/providers/README.md#live-testing-status)).

### Added

**Core SDK**

- `ai.GenerateText` / `ai.StreamText`: text generation with an automatic
  multi-step tool-calling loop (`MaxSteps`, `StopWhen`, `ai.StepCountIs`,
  `PrepareStep`, `OnStepFinish`), conversation continuation via
  `GenerateTextResult.Messages`, and a shared retry wrapper
  (`MaxRetries`, `*ai.RetryError`).
- `ai.GenerateObject[T]` / `ai.StreamObject[T]`: structured output with a
  JSON Schema derived from Go structs by reflection, native-JSON mode
  where a provider supports it and forced-tool-call mode otherwise.
- `ai.Embed` / `ai.EmbedMany`: single and batched embeddings, with
  automatic batching against each provider's `MaxBatchSize()`, and
  `ai.CosineSimilarity` for comparing vectors.
- `ai.NewTool[Args]`: typed tool definitions with a reflection-derived
  JSON Schema; the tool loop's error taxonomy
  (`*ai.NoSuchToolError`, `*ai.InvalidToolArgumentsError`,
  `*ai.ToolExecutionError`) and `RepairToolCall` (one retry at fixing a
  failing call before the normal error path).
- `ai.ActiveTools`: restricts which of `Tools` are offered to the model
  and treated as known during execution.
- Stream lifecycle callbacks: `OnChunk`, `OnFinish`, `OnError` (with the
  argument-validation exclusion rule) on both `GenerateText` and
  `StreamText`.
- `ai.SmoothStream`: re-chunks `TextDelta`s into word- or line-granularity
  pieces for a steadier UI cadence, with no implicit default delay.
- Reasoning/thinking content: `provider.ReasoningPart` (non-streaming),
  `provider.ReasoningDelta`/`ReasoningEnd` (streaming), and
  `Result.ReasoningText`/`Stream.ReasoningText()`.
- Sources/grounding: `provider.SourcePart` and `provider.SourceEvent`
  (Google `groundingMetadata` today).
- `provider.ProviderOptions` / `ProviderMetadata`: the namespaced,
  raw-wire-key escape hatch for provider-specific request parameters and
  response data, threaded through `Call`, `Response`, and `FinishPart`.
- Middleware: `ai.ExtractReasoningMiddleware`, `ai.SimulateStreamingMiddleware`,
  `ai.DefaultSettingsMiddleware`, `ai.WrapModel`.
- `ai.Registry`: resolves `"provider:model"` strings into concrete
  language, embedding, image, speech, and transcription models.
- `ai.Telemetry` / `ai.TelemetryMiddleware`: a minimal, dependency-free
  span-reporting seam (`OnSpanStart`/`OnSpanEnd`) for bridging to
  OpenTelemetry or any other tracing system.
- File attachments: `provider.FilePart{Data, MediaType, Filename}` for
  user messages, with provider-specific support (PDF-only, any media
  type, a fixed document-type set, or unsupported, depending on the
  provider).
- `provider.ProviderMetadata` population from every provider that reports
  extra response data (Anthropic prompt-cache token counts,
  `openaicompat`'s `system_fingerprint`).

**Providers (22)**

- Wave 1 — OpenAI, Anthropic, Google (Gemini): full, independent
  implementations proving the provider abstraction across three distinct
  wire formats.
- Wave 2 — Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity
  as thin presets over the extracted `internal/openaicompat` base; Mistral
  and Cohere as full, standalone implementations.
- Wave 3 — Azure OpenAI (API-key preset over `internal/openaicompat`),
  Vertex AI (Google service-account/ADC auth, `internal/geminicompat`
  extracted from the `google` provider), Amazon Bedrock (Converse API,
  AWS SigV4 request signing, `eventstream` framing).
- Each provider implements `provider.LanguageModel` and, where the
  vendor's API supports it, `provider.EmbeddingModel`; all pass the
  shared `provider/providertest` conformance suite.

**Media capabilities**

- `ai.GenerateImage`, `ai.GenerateSpeech`, `ai.Transcribe` entry points
  and their `provider.ImageModel` / `SpeechModel` / `TranscriptionModel`
  interfaces.
- OpenAI (image, speech, transcription), Google/Vertex AI (Imagen image
  generation), xAI (image), Groq (transcription), and ElevenLabs (speech
  + transcription) implementations.
- Wave 8 — fal and Replicate (synchronous image generation), Luma
  (asynchronous, poll-until-terminal image generation via
  `WithPollInterval`), Deepgram (transcription, raw-audio request body),
  and LMNT and Hume (speech synthesis), closing out the Vercel-supported
  media roster targeted for v0.1. All six are implemented against
  documented wire formats and covered by fixture-based unit tests, but
  not yet smoke-tested against the live APIs — see
  [Provider overview: Live-testing status](docs/providers/README.md#live-testing-status).

**Core parity features**

- `provider.ToolChoice`, `Call.Temperature`/`TopP`/`StopSequences`, usage
  detail fields (cached/reasoning tokens).
- `ai.DefaultSettingsMiddleware` provider-options merge semantics.
- Provider-metadata plumbing through both `Generate` and `Stream` paths.

**MCP (Model Context Protocol)**

- Package `mcp`: a v1, tools-only MCP client over stdio
  (`mcp.NewStdioTransport`) and Streamable HTTP
  (`mcp.NewStreamableHTTPTransport`) transports, pinned to protocol
  version `2025-03-26`.
- `mcp.Tools(ctx, client)`: adapts a server's tools into `[]ai.Tool` for
  direct use in `GenerateTextOpts.Tools`, including `IsError` tool
  results surfacing as `*ai.ToolExecutionError`.

**Documentation**

- Getting-started guide, per-topic core guides (`generating-text`,
  `tools`, `structured-output`, `embeddings`, `media`, `streaming`,
  `reasoning`, `middleware-and-registry`, `provider-options`,
  `errors-and-retries`, `telemetry`), MCP guide, and a full
  provider-reference page per vendor.
- [Migrating from the Vercel AI SDK](docs/migrating-from-vercel-ai-sdk.md)
  and [Architecture](docs/architecture.md).

[Unreleased]: https://github.com/azrtydxb/go-ai-sdk/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/azrtydxb/go-ai-sdk/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/azrtydxb/go-ai-sdk/releases/tag/v0.1.0
