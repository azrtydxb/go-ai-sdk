# v6 parity final audit

**Status: closed.** This is the closing artifact of the
[AI SDK 6 parity roadmap](../plans/2026-08-03-v6-parity-roadmap.md)
(waves 9–14, 2026-08-03). It records what "full parity with the AI SDK 6
core" means concretely for `go-ai-sdk` as of wave 14: the complete
have-list, what's deliberately out of scope forever, and the AI SDK 7-only
items confirmed not targeted by this effort. For the feature-by-feature
status table (kept as the living reference — this document is a snapshot,
that page gets updated as things change), see
[Migrating from the Vercel AI SDK § AI SDK 6 delta](../../migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta).

## How this audit was run

Wave 14 began with a gap re-audit against ai-sdk.dev (snapshot
2026-08-03), cross-checked line by line against the existing delta table
built up over waves 9–13. The finding: the port was **already essentially
at parity** going into wave 14. Four genuine core-surface gaps remained,
each closed this wave (tasks 1–5 of the wave-14 plan):

1. An OpenTelemetry bridge (the delta table had OTel listed as "planned").
2. Tool `strict` mode and `inputExamples`.
3. Per-tool input-streaming lifecycle hooks (`onInputStart`/`onInputDelta`/
   `onInputAvailable` in Vercel's naming).
4. Structured `timeout` bounds (Vercel's distinct connect/response/total
   object; modeled here as Total/Step/Chunk instead — see the mapping
   note in the delta table).

Closing all four is what "full v6 core parity" in this document's title
means: not that every literal AI SDK 6 API surface exists byte-for-byte
(some are deliberately re-shaped for Go, see the divergences in the
migration guide), but that every feature *category* the v6 core exposes
has a `go-ai-sdk` equivalent, documented, tested, and shipped.

## The have-list: AI SDK 6 core surface covered

Grouped by area; each links to its guide, which is the source of truth for
exact behavior. This list intentionally does not repeat every field-level
divergence — see
[Migrating from the Vercel AI SDK § Documented divergences](../../migrating-from-vercel-ai-sdk.md#documented-divergences)
for those.

**Text generation & the tool loop**
- `GenerateText`/`StreamText` core loop: `MaxSteps`, `StopWhen`
  (`StepCountIs`, `HasToolCall`, `LoopFinished`), `PrepareStep`
  (model-swap-with-persistence semantics), full call settings (`TopK`,
  `PresencePenalty`, `FrequencyPenalty`, `Seed`, `Headers`), retries
  (`*ai.RetryError`), conversation continuation via `Messages`.
- Structured `Timeout{Total, Step, Chunk}` / `*ai.TimeoutError`, with the
  SDK-bound-vs-caller-ctx-abort distinction (wave 14).
- `OnAbort` (mid-stream ctx cancellation / consumer abandonment).
- Full lifecycle-callback event set: `OnModelCallStart`/`OnModelCallEnd`,
  `OnToolExecutionStart`/`OnToolExecutionEnd`, `OnStepFinish`, `OnFinish`,
  `OnError`.
- Output modes on `GenerateText` (`OutputObject[T]`/`OutputArray[T]`/
  `OutputChoice`/`OutputJSON`, `OutputAs[T]`) — `StreamText`'s
  partial-output streaming variant is the one acknowledged future item,
  tracked below, not a parity gap.

**Tools**
- `NewTool[Args]` reflection-derived JSON Schema, `ActiveTools`,
  `RepairToolCall`, the typed execution-error taxonomy
  (`InvalidToolArgumentsError`/`ToolExecutionError`/`NoSuchToolError`/
  `ToolApprovalDeniedError`).
- Multi-modal tool results (`ToolResultContent`).
- Tool-execution approvals: `RequireApproval`/`ApprovalRequirer`,
  `Approvals`/`ApproveToolCall`, `PendingApprovals`, the resumable
  suspend-then-resume flow.
- `RuntimeContext` threaded into tool execution and approval checks.
- Tool `strict` mode (`WithToolStrict`) and `inputExamples`
  (`WithToolInputExamples`, `AddToolInputExamplesMiddleware`) — wave 14.
- Per-tool input-streaming lifecycle hooks (`WithToolInputCallbacks`,
  `OnInputStart`/`OnInputDelta`/`OnInputAvailable`) — wave 14.

**Structured output & embeddings**
- `GenerateObject[T]`/`StreamObject[T]` (native-JSON and forced-tool-call
  modes).
- `Embed`/`EmbedMany` (automatic batching), `CosineSimilarity`.
- `Rerank`/`provider.RerankingModel` (Cohere, Voyage, Mixedbread).

**Streaming**
- `TextStream.Parts()` (`iter.Seq[provider.StreamPart]`): text, tool-call,
  reasoning, source, and finish parts, uniformly across providers.
- `SmoothStream` (word/line chunking, explicit `Delay`).
- Suspension in streams (approvals) with the same semantics as
  `GenerateText`.

**Reasoning**
- Unified `Reasoning` request option (`Effort`/`BudgetTokens`), mapped per
  provider; `ReasoningPart`/`ReasoningDelta`/`ReasoningEnd` surfaced
  uniformly across every provider that supports it.

**Middleware & registry**
- `ExtractReasoningMiddleware`, `SimulateStreamingMiddleware`,
  `DefaultSettingsMiddleware`, `ExtractJSONMiddleware`,
  `AddToolInputExamplesMiddleware` (wave 14) — five `provider.LanguageModel`
  middlewares.
- `WrapModel`/`WrapImageModel` naming hooks.
- `Registry` (`"provider:model"` resolution across six model kinds).

**Agents & Code Mode**
- `agent.Agent` (`Generate`/`Stream`, `RunOpts`, its own `MaxSteps`
  default), `agent.AsTool` for sub-agent delegation.
- `codemode.Tool`/`Sandbox`/`APIDoc`.

**Media**
- Image, video (`GenerateVideo`), speech, transcription (including live
  `StreamTranscribe`), and audio translation (`Translate`, REST-only).
- File/skill upload (`UploadFile`/`DeleteFile`/`FileStore`,
  Anthropic's `UploadSkill`).
- A minimal OpenAI realtime voice session (`RealtimeSession`).

**MCP**
- Tools, resources/resource templates, prompts, argument completions,
  server-initiated elicitation (stdio-only), token-provider auth with
  transient HTTP retry.

**Observability**
- `ai.Telemetry`/`ai.TelemetryMiddleware` — the dependency-free seam,
  now `ctx`-aware with a `CorrelationID` for reliable span pairing (the
  program's one breaking change, wave 14).
- `contrib/otel` — a real OpenTelemetry bridge shipping GenAI-semconv
  spans, as its own nested Go module so the root stays zero-dependency
  (wave 14).

**Provider fleet**
- 39 providers across chat/tools/structured-output/embeddings/reranking/
  images/video/speech/transcription, per the
  [provider overview](../../providers/README.md).

## Deliberately out of scope (permanent)

These are standing scope rulings from the roadmap, not gaps — they will
not be revisited under a "v6 parity" framing:

- **UI framework hooks** (`useChat`, `useCompletion`, `useObject`, and the
  rest of `@ai-sdk/react`/`@ai-sdk/svelte`/etc.) — framework-bound
  client-side state managers with no Go equivalent target; `go-ai-sdk` is
  a backend/server SDK.
- **React Server Components streaming** (`streamUI`, `createStreamableUI`,
  other `ai/rsc` primitives) — tied to React's RSC runtime.
- **MCP Apps rendering** — a UI-rendering layer on top of MCP, out of
  scope for the same reason as the UI hooks above.
- **DevTools / Terminal UI** — out-of-scope tooling; this SDK ships a
  library, not a devtools/inspector product.
- **WebRTC realtime transport** — skipped in favor of a stdlib-only
  RFC 6455 WebSocket client (`internal/websocket`); every realtime/
  streaming-transcription feature in this SDK is WebSocket-based, not
  WebRTC-based.

See [Migrating from the Vercel AI SDK § Features NOT ported](../../migrating-from-vercel-ai-sdk.md#features-not-ported)
for the canonical, maintained version of this list.

## Confirmed v7-only: not targeted by this program

Found during the wave-14 gap re-audit: a handful of items visible on
ai-sdk.dev as of the 2026-08-03 snapshot belong to **AI SDK 7**, not 6.
They are explicitly **not targeted** by the v6 parity program — there is
no "gap" here to close, because they were never in scope:

- **`WorkflowAgent`** and a `toolOrder` execution-order hint on multi-tool
  steps.
- **`contextSchema`** — a schema attached to an agent/run's context object
  itself, distinct from `RuntimeContext`'s untyped bag.
- **A top-level `reasoning` enum** on a message/part — a
  response-shape-level concept, distinct from the *request*-side
  `Reasoning{Effort, BudgetTokens}` option this SDK already ships.
- **A redesigned tool-execution approvals surface** — v7 reworks the
  approval message-part shape beyond what this SDK's
  `PendingApprovals`/`Approvals` models today.

If a v7 parity effort is ever chartered, this is the starting punch list.
Until then, these remain out of scope by the same reasoning that keeps
the "permanent" list above out of scope: not a gap in v6 parity, a
different target entirely.

## One acknowledged future item (not a v6 gap)

`StreamText` returns `ai.ErrOutputWithStreamText` immediately if
`GenerateTextOpts.Output` is set — Vercel's `Experimental_Output` also
supports streaming partial output incrementally. `GenerateText`'s `Output`
modes are fully shipped; only `StreamText`'s partial-streaming variant
remains. This was already tracked before wave 14 (see
[Migrating from the Vercel AI SDK § Future — plausible, not yet implemented](../../migrating-from-vercel-ai-sdk.md#future--plausible-not-yet-implemented))
and is called out here only for completeness, not as a new finding — it
does not affect the "full v6 core parity" determination, since it isn't
part of the core generate/stream contract Vercel's own docs treat as v6
baseline (it's an experimental extension in both SDKs).

## Release procedure: root v0.2.0 + contrib/otel v0.2.0

`contrib/otel/go.mod` currently requires `github.com/azrtydxb/go-ai-sdk
v0.1.0` (with a dev-only `replace github.com/azrtydxb/go-ai-sdk => ../..`
that makes the branch build against the local root module regardless of
that require line). v0.1.0 predates this wave's `OnSpanStart(ctx, ...)`
signature and `CorrelationID` additions to `ai.Telemetry`/`ai.SpanInfo`,
so **do not bump the require to `v0.2.0` yet** — no such tag exists, and a
premature bump would break `go build` for anyone resolving modules
normally (only the local replace papers over it today).

The tag-time sequence, in order, is:

1. Tag the root module first: `git tag v0.2.0` (on the commit that ships
   this wave's CHANGELOG entry) and push the tag.
2. Only after that tag exists, edit `contrib/otel/go.mod`: bump
   `require github.com/azrtydxb/go-ai-sdk` from `v0.1.0` to `v0.2.0`. The
   `replace ... => ../..` directive may be kept for local development (it
   only affects builds run from within this repo checkout) or removed —
   either way, `go mod tidy` inside `contrib/otel` should be run to verify
   the require resolves and the go.sum is consistent.
3. Commit that go.mod/go.sum change, then tag the submodule:
   `git tag contrib/otel/v0.2.0` and push it.

Until step 3 has happened, external consumers running `go get
github.com/azrtydxb/go-ai-sdk/contrib/otel` resolve the untagged
submodule to whatever pseudo-version its latest commit produces, and that
commit's `go.mod` still requires root `v0.1.0` — which lacks the symbols
`contrib/otel`'s code calls. Until the bump-and-tag above lands, external
consumers must either add their own
`replace github.com/azrtydxb/go-ai-sdk => github.com/azrtydxb/go-ai-sdk
v0.2.0` (or a local path) in their own go.mod, or depend on a commit
pseudo-version of `contrib/otel` from after the bump. `contrib/otel/README.md`
has been corrected to state this accurately instead of claiming
tagged-consumer resolution works today.

## Source of truth

- [Migrating from the Vercel AI SDK § AI SDK 6 delta](../../migrating-from-vercel-ai-sdk.md#ai-sdk-6-delta) —
  the living, feature-by-feature status table this snapshot is drawn from
- [v6 parity roadmap](../plans/2026-08-03-v6-parity-roadmap.md) — the
  wave-by-wave plan (waves 9–14) that reached this state
- [CHANGELOG.md § 0.2.0](../../../CHANGELOG.md#020--2026-08-03) — the
  release this wave shipped as
- [Architecture](../../architecture.md) — how the observability layer and
  `contrib/otel`'s nested-module split fit into the codebase
- Wave 14 task reports (`.superpowers/sdd/2026-08-03-go-ai-sdk-wave14/task-{1..6}-report.md`) —
  the detailed per-task implementation record this audit summarizes
