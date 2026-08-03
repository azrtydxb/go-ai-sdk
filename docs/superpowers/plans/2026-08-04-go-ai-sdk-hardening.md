# go-ai-sdk Post-v0.2.0 Hardening Plan (security, correctness, concurrency)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Fix every confirmed real bug from a multi-dimension audit (security, concurrency, correctness — top-level + deep per-package sub-audits) plus carried DRY debt, with no behavior regressions. Targets v0.2.1.

**Audit verdict:** the codebase is unusually disciplined. Verified CLEAN and OUT OF SCOPE — do not touch: TLS config, AWS SigV4 (botocore-vector-verified), gauth JWT claims + token cache single-flight, credential handling (no keys in logs/URLs/errors), `Call.Headers` CRLF (net/http validates), eventstream binary bounds, websocket framing, partialjson, the tool-loop/output/timeout/approval composition, registry, agent package, error Unwrap chains, nil-deref/bounds across all 39 providers, body-close discipline.

## Global Constraints

- Root module stays **zero-dependency** (stdlib only); ADDITIVE except where a fix is a documented behavior change (SSRF rejection, empty-args normalization). contrib/otel untouched.
- Providers never retry; ctx passthrough; existing conventions preserved.
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` (+`-race` on ai / mcp / aitest / internal/websocket / the WS providers when touched).
- Commits conventional, trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: ai-core correctness — no-arg tool calls, middleware ProviderMetadata, suspend-timeout leak

**Files:** `ai/tool.go`, `ai/middleware.go`, `ai/stream_text.go`, `ai/middleware_json.go`/`ai/telemetry.go`/`ai/generate_text.go` (nil-model guards) (+ tests).

1. **HIGH — no-argument tool calls always fail.** `ai/tool.go` `Execute` does `json.NewDecoder(bytes.NewReader(args)).Decode(...)`; when `args` is empty/nil (the normal wire shape for a no-arg tool, and exactly what stream assembly produces when no `ArgsDelta` arrives — `stream_text.go` builds `ToolCallPart{Args: pc.args}` with nil `pc.args`), `Decode` returns `io.EOF` → every no-arg call is rejected as `*InvalidToolArgumentsError`; in streaming a no-arg tool can never succeed. Fix: normalize empty/whitespace-only args to `[]byte("{}")` before decoding: `if len(bytes.TrimSpace(args)) == 0 { args = []byte("{}") }`. Test: a tool taking `struct{}{}` called with `""`, nil, and `"  "` all execute; a tool needing fields still errors on `""` only if the fields are required (schema-level, unchanged).
2. **MEDIUM — ProviderMetadata dropped by two middlewares.** `extractReasoningFromResponse` (`middleware.go`) rebuilds `*provider.Response` copying Content/FinishReason/Usage/Raw but omits `ProviderMetadata`; `SimulateStreamingMiddleware.Stream` synthesizes `FinishPart{Reason,Usage}` with no `ProviderMetadata` and drops `resp.Raw`/`ProviderMetadata`. `ExtractJSONMiddleware` correctly preserves it. Fix: carry `ProviderMetadata` through in `extractReasoningFromResponse`'s returned Response, and set `FinishPart.ProviderMetadata: resp.ProviderMetadata` in the simulated stream. Test: wrap a model reporting ProviderMetadata with each middleware → metadata survives to the result/FinishPart.
3. **MEDIUM — immediate-suspend stream leaks the Total-timeout context.** `ai/stream_text.go` resume-batch-pending path returns `s` with `pendingApprovals` set but only releases `cancelTotal` via `Parts()`/`Close()`; a caller that reads `PendingApprovals()` then drops the stream leaks the Total context/timer. Fix: call `s.cancelTotal()` (and stop any armed watchdog) before returning on that path — no stream will run. Test: Timeout.Total set + resume suspends + caller reads PendingApprovals without Parts/Close → no leaked timer (goroutine/context check).
4. **LOW — nil-model middleware guards.** `WrapModel`/`WrapImageModel`/`TelemetryMiddleware`/`ExtractReasoningMiddleware`/`SimulateStreamingMiddleware`/`DefaultSettingsMiddleware`/`ExtractJSONMiddleware`/`AddToolInputExamplesMiddleware` defer a nil-model panic to first call. Fix: panic early with a clear message (`ai: <name>: nil model`) in each constructor. Test: one representative constructor panics with the message on nil.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on ai). Commit** — `fix(ai): no-arg tool calls, middleware ProviderMetadata, suspend-timeout leak, nil-model guards`

---

### Task 2: Provider correctness — geminicompat thought flag, bedrock signed-header clobber, cohere count validation

**Files:** `internal/geminicompat/wire.go` + `internal/geminicompat/language_model.go`, `providers/bedrock/language_model.go`, `providers/cohere/embedding.go` + `providers/cohere/rerank.go` (+ tests).

1. **HIGH — geminicompat emits reasoning as answer text.** With `call.Reasoning` set, geminicompat sends `thinkingConfig.includeThoughts:true` and Gemini returns thought-summary parts marked `"thought":true`, but `wirePart` has no `thought` field, so those parts fall into the `Text != ""` branch and are yielded as `TextDelta`/`TextPart` — internal reasoning is concatenated into the visible answer (google + vertex, streaming + non-streaming). Fix: add `Thought bool `json:"thought,omitempty"`` (and `ThoughtSignature string `json:"thoughtSignature,omitempty"`` if present) to `wirePart`; in both stream (`language_model.go`) and non-stream (`wire.go`) conversion, route `part.Thought == true` text to `ReasoningDelta`/`ReasoningPart` instead of Text. Test: a fixture response with a `thought:true` part and an answer part → the thought becomes ReasoningText, the answer becomes Text (both stream + non-stream).
2. **MEDIUM — bedrock user Content-Type applied after SigV4 → guaranteed 403.** `bedrock/language_model.go` signs with `Content-Type: application/json` in the canonical request, but any `Call.Headers` entry that isn't `x-amz-*` (incl. `Content-Type`) goes into the `unsigned` map and is `Header.Set` AFTER `sigv4.Sign`, overwriting a signed header on the wire → `SignatureDoesNotMatch`. Fix: exclude `Content-Type` (case-insensitive) from the caller-header override for bedrock (the provider owns it), OR apply it before signing like the x-amz-* branch. Simplest correct: skip a caller `Content-Type` for bedrock with a comment (it's provider-owned, same rationale as the auth header). Test: a Call with `Headers{"Content-Type":"x"}` → the signed request still carries the provider's Content-Type and the signature covers it (assert the on-wire Content-Type is application/json and no post-sign override).
3. **LOW/MEDIUM — cohere Embed doesn't validate response count.** `cohere/embedding.go` returns `wr.Embeddings.Float` as-is; a short/mismatched response silently returns fewer embeddings than inputs, mis-zipping downstream. Fix: add the same count check vertex/geminicompat use (`len(embeddings) != len(inputs)` → error). Also `rerank.go` passes server `Index` through unvalidated against `len(call.Documents)` — clamp/skip out-of-range like ai.Rerank already does defensively (verify; if ai layer already guards, a comment suffices). Test: a fixture returning fewer embeddings than inputs → error.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(providers): gemini thought→reasoning, bedrock Content-Type signing, cohere count validation`

---

### Task 3: Security — internal/fetchmedia (SSRF + size) for server-controlled URL fetches

**Files:** Create `internal/fetchmedia/fetchmedia.go` (+test). Modify `providers/luma/video.go`, `providers/fal/video.go`, `providers/replicate/video.go` (replace the 3 byte-identical `fetchVideo`/`parseMediaTypeVideo`), `providers/bfl/image.go` (poll-URL origin gate + sample fetch), `internal/fetchimage/fetchimage.go` (route through fetchmedia or add identical guards; keep imagesniff fallback).

**Vulnerabilities:** BFL sends the `x-key` API key to a server-chosen absolute `polling_url` (High credential-leak/SSRF); all media fetches fetch server-chosen URLs with no scheme/host validation, follow redirects into private/link-local ranges incl. cloud metadata `169.254.169.254` (Medium SSRF); those paths `io.ReadAll` unbounded (Medium memory-DoS).

**Produces:**
```go
package fetchmedia
const MaxBytes = 256 << 20 // default body ceiling
// Fetch GETs url with SSRF + size protection: http(s) only; reject any host
// resolving to link-local/metadata (169.254.0.0/16, fe80::/10) — re-checked
// on every redirect hop; ≤10 redirects; read ≤ maxBytes (0→MaxBytes), error
// if exceeded. errPrefix is added ONCE (fixes the existing double-prefix).
func Fetch(ctx context.Context, client *http.Client, url, errPrefix string, maxBytes int64) (data []byte, mediaType string, err error)
// SameOrigin reports scheme+host equality; gates credential attachment.
func SameOrigin(base, candidate string) bool
```
Notes: scheme allowlist http/https; resolve host via `net.DefaultResolver.LookupIPAddr(ctx, host)`, reject if ANY IP is link-local/metadata (do NOT block generic private ranges — self-hosted CDNs are legit; link-local is the crown-jewel with near-zero false positives); build a per-call `*http.Client` copy sharing the caller's Transport with `CheckRedirect` enforcing the scheme+link-local re-check and a 10-hop cap (never mutate the caller's client); size cap via `io.ReadAll(io.LimitReader(body, maxBytes+1))`. Callers must NOT re-wrap with their own "fetch video:" prefix (removes the `luma: fetch video: luma: fetch …` double-prefix). BFL: gate the credentialed poll on `SameOrigin(baseURL, pollingURL)` — mismatch → error, never attach `x-key`; also apply link-local check + a size cap to the poll body. fetchimage: call fetchmedia.Fetch then apply its existing sniff fallback (keep luma-image + bfl-sample behavior).

**Tests:** fetchmedia — happy; non-http rejected; literal 169.254.169.254 rejected pre-request; 302→link-local rejected; body over cap rejected; SameOrigin cases. BFL — foreign-origin polling_url → error + no x-key reaches the foreign host (fixture records header); same-origin poll works. luma/fal/replicate — existing fixtures pass unchanged; error messages single-prefixed.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(security): SSRF + size guards on server-controlled media fetches`

---

### Task 4: Security — bounded reads: SSE accumulation + MCP success body

**Files:** `internal/sse/sse.go` (+test), `mcp/http.go` (+test).

- `internal/sse/sse.go`: `dataLines` accumulates across `data:` lines until a blank line — a server streaming endless data lines and never a blank line grows unbounded → OOM (reachable on every streaming provider + MCP drainSSE). Fix: track accumulated event bytes; over a ceiling (`MaxEventBytes`, default 32 MiB) → yield an error via the iterator and stop. Keep the per-line 10 MB scanner buffer.
- `mcp/http.go`: success-body `io.ReadAll` is unbounded (error bodies already `LimitReader(64KiB)`). Fix: `io.LimitReader` the success body (16 MiB), error if exceeded.

**Tests:** sse — over-ceiling event → iterator error, no OOM; normal multi-line events still assemble. mcp — success body over cap → error; normal bodies fine.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(security): cap SSE event accumulation and MCP success-body reads`

---

### Task 5: Security — multipart CRLF-injection guard

**Files:** `providers/openai/files.go`, `providers/anthropic/files.go`, `providers/anthropic/skills.go`, and sweep `providers/revai`, `providers/gladia`, `providers/assemblyai` multipart builders. Create `internal/multipartutil/multipartutil.go` (validator) if 3+ callers need it.

**Vulnerability:** `multipart.Writer` writes MIME headers verbatim with no CRLF validation. `h.Set("Content-Type", mediaType)`+`CreatePart` with a caller `MediaType`, and `WriteField(k, …)` with ProviderOptions keys — a `\r\n` forges extra parts (audit-confirmed). Fix: reject any caller-controlled MediaType / Filename / field name / field value containing CR, LF, or `"` before building the part (`invalid multipart value: contains CR, LF, or quote`). Apply everywhere external strings reach a multipart writer across the swept providers.

**Tests:** UploadFile (openai+anthropic) with `MediaType` containing `\r\n` → error, nothing sent; ProviderOptions key with `\n` → error; normal uploads unaffected; same-shape for other swept multipart providers.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(security): reject CRLF/quote in multipart headers and field names`

---

### Task 6: Concurrency/WS — RealtimeSession conn-close, control-frame write deadline, base64-delta surfacing

**Files:** `providers/openai/realtime.go`, `internal/websocket/websocket.go` (+ tests, incl. a regression test in each WS provider pkg).

1. **MEDIUM — RealtimeSession.readLoop leaks the TCP connection.** Both sibling readLoops carry `defer s.conn.Close(websocket.CloseNormal, "")` (deepgram/live.go, openai/realtime_transcription.go); realtime.go's is missing it → on decode-error/ctx-cancel the socket lingers until a Close() that may never come. Fix: add the same defer. Add a regression test in deepgram + both openai WS packages asserting the underlying conn is closed after a readLoop-terminating event WITHOUT a caller Close (server observes EOF) — pins the divergence.
2. **LOW — websocket auto-pong write can block outside ctx.** Inside `Read(ctx)` the control-frame write (`writeControl`→`writeFrame`) has no write deadline; a peer that pings then stops draining can wedge Read where ctx can't reach. Fix: give control-frame writes a bounded `SetWriteDeadline`.
3. **LOW — OpenAI realtime audio-delta base64 failure swallowed.** `realtime.go` `if b, err := ...; err == nil` yields an event with nil AudioDelta indistinguishable from empty on a corrupt delta. Fix: surface the decode failure (record via setErr, or document) — pick surfacing via the event's error/Raw with a doc note; minimal: keep Raw + document, or setErr. Choose setErr-free: attach nothing but document that a decode failure yields Raw-only; simplest acceptable is a doc note — but prefer recording it so callers can detect. Implement: on decode error, still deliver the event with Raw set and leave AudioDelta nil, AND note it in the doc comment (no behavior break). (If cheap, add a per-event decode-error field — but avoid API churn; doc note is acceptable.)

**Tests:** (1) each WS provider: readLoop-terminating event without caller Close → conn closed. (2) websocket: control writes carry a deadline (unit). (3) realtime: corrupt audio delta → event delivered, Raw present, no panic. -race on all touched.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race). Commit** — `fix: WS conn-close on readLoop exit, bounded control-frame writes, surface realtime base64 errors`

---

### Task 7: aitest mocks concurrency-safety

**Files:** `ai/aitest/mock.go` (+test).

**Issue:** every mock appends to its record slice with no lock — harmless in-repo but external users sharing one mock across concurrent calls get torn/lost records. Fix: add a `sync.Mutex` per mock guarding record-append; keep exported record fields for single-goroutine reads; add locked snapshot accessors (e.g. `RecordedCalls() []provider.Call`); document direct-field access as single-goroutine-only. Don't break existing tests reading `.Calls`.

**Tests:** a mock driven from N concurrent goroutines under -race records N calls, no race; existing tests unaffected.

- [ ] **Step 1: Failing (race) test → implement → green under -race. Full check suite (-race on ai/aitest + ai). Commit** — `fix(aitest): concurrency-safe mock call recording`

---

### Task 8: internal packages — retry, gauth, schema

**Files:** `internal/retry/retry.go`, `internal/gauth/gauth.go`, `internal/schema/schema.go` (+ tests).

1. retry `defer timer.Stop()` inside the loop → inline stop per iteration (no defer-in-loop).
2. `calculateBackoff` panics if `BaseDelay<=0` (`rand.Int63n`) → guard `if baseDelay <= 0 { return 0 }`.
3. gauth `rsa.SignPKCS1v15(nil, …)` → pass `crypto/rand.Reader` (restores blinding/side-channel protection).
4. gauth token-endpoint 5xx returns plain `fmt.Errorf`, not `*ai.APICallError` → non-retryable inside Vertex retry. Fix: wrap non-2xx from the token endpoint in `ai.NewAPICallError` with correct retryable classification (this makes internal/gauth import ai — check for an import cycle; ai imports provider, provider is standalone, gauth is internal used by geminicompat/vertex; if ai↔gauth cycles, instead return a typed error gauth defines that the provider layer maps, OR have gauth return an error implementing a retryable interface the retry layer already checks. SIMPLEST cycle-free: gauth returns a sentinel/typed error with an `IsRetryable() bool`; verify retry.Do checks an interface, not the concrete ai.APICallError — if it only checks ai.APICallError via errors.As, then the provider calling gauth must translate. Investigate and pick the cycle-free path; document.)
5. internal/schema misdescribes `[]byte` (reflected as array-of-integer though json marshals base64 string) and `time.Time`/`json.Marshaler` structs (expanded structurally though they marshal as a JSON string). Fix: special-case `[]byte` → `{"type":"string"}` (note: json base64), and `time.Time` → `{"type":"string","format":"date-time"}`; more generally, a type implementing `json.Marshaler` whose zero marshals to a JSON string should be `{"type":"string"}` — but keep it targeted to `[]byte` and `time.Time` to avoid over-reach (document the limitation for other Marshalers). Test: a struct with a `[]byte` and a `time.Time` field → schema shows string types; round-trip a matching JSON value unmarshals.

**Tests:** per fix above; existing retry/gauth/schema tests stay green.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(internal): retry footguns, gauth signing RNG + retryable token errors, schema []byte/time.Time`

---

### Task 9: MCP robustness — stdio ctx write, shutdown/leak, HTTP retry idempotency

**Files:** `mcp/stdio.go`, `mcp/jsonrpc.go`, `mcp/http.go`, `mcp/client.go` (+ tests).

1. **HIGH/MED — stdio Send ctx-blind wedge.** `Send` checks ctx once then bare `t.w.Write` under `writeMu`; a hung child (full stdin buffer) blocks forever, and because `Client.call` does Send synchronously under `sendMu`, it wedges every concurrent RPC despite ctx deadlines. Fix: make the write ctx-aware — if the writer is an `*os.File`, `SetWriteDeadline` from ctx (clear after); if no ctx deadline, a ctx.Done watcher sets a past deadline; abandon the transport on cancel (partial line corrupts framing). Fall back gracefully for the in-memory test transport. Test: a blocking writer → Send returns on ctx cancel; subprocess round-trip still works.
2. **MED — transport error path never closes the transport.** `recvLoop` read-error → `closeWith` cancels `c.ctx` but never calls `transport.Close()` → zombie child + parked stdio readLoop goroutine. Fix: `closeWith` (or the recvLoop error path) also calls `c.transport.Close()` (idempotent via closeOnce). Test: simulate a transport read error → transport.Close() invoked, no parked goroutine.
3. **MED — Close only sweeps SSE bodies; a hung JSON body survives.** `mcp/http.go` registers only `text/event-stream` bodies in `openBodies`; a stalled `application/json` body read isn't interrupted by Close. Fix: track the JSON-body case too (register before read, untrack after) so Close's sweep closes it. Test: a stalled JSON body → Close interrupts it.
4. **MED — HTTP retry double-executes non-idempotent requests.** Any `client.Do` error → `retryableHTTPError` → re-POST; but Do can fail after the server processed the POST (reset while reading headers) → a side-effecting `tools/call` runs twice. Fix: restrict transport-error retry to errors that prove the request wasn't delivered (e.g. `syscall.ECONNREFUSED`, DNS errors) — a conservative allowlist — and do NOT retry a generic post-connection error; still retry 429/503 (server-sent, safe). At minimum, document the hazard on `WithHTTPRetry`. Prefer the conservative-allowlist fix. Test: a mid-response connection error is NOT retried; a 429 and a connection-refused ARE retried.
5. **LOW — `nextID int64` atomic alignment on 32-bit.** Use `atomic.Int64` (or move the field to the struct top). Fix.
6. **LOW — call-after-death returns generic error.** `call` fabricates `errClosedMsg` instead of `c.closeErr` (which holds the real cause). Fix: return `c.closeErr` when closed. Test.
7. **LOW — paginate infinite loop on repeated cursor.** `client.go` `paginate` has no repeat-cursor/page cap. Fix: error if `next == cursor`, or cap pages. Test.
8. **LOW — tools/list + tools/call not capability-gated.** Add the same `hasCapability("tools")` gate the resources/prompts/completions methods use. Test.
9. **LOW — sendMu held across HTTP retry backoff (HOL blocking).** `Client.call` holds `sendMu` for the whole Send incl. retry sleeps. Fix: httpTransport is internally synchronized — do the retry/backoff loop without requiring the caller's sendMu to span all attempts (or lock per-attempt inside the transport). Investigate: sendMu is in jsonrpc Client, retry loop is in http.go's Send — the lock spans one Send() call. Moving retry outside sendMu means Client.call can't hold sendMu across Send. Assess whether serialization is required for correctness (stdio needs it for framing; http doesn't — http is internally synchronized). Option: only hold sendMu for stdio-style transports; or accept the HOL and just cap backoff. SIMPLEST safe: document + keep (backoff is bounded). Given complexity/risk, downgrade this to a doc note unless a clean fix emerges. Implementer's call — do the clean fix only if low-risk; else document.

**Tests:** per fix; full mcp suite + -race green.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `fix(mcp): ctx-aware stdio write, transport/body shutdown leaks, conservative HTTP retry, robustness`

---

### Task 10: MCP dispatch bound + Close drain + elicitation version negotiation + handler-error

**Files:** `mcp/jsonrpc.go`, `mcp/client.go`, `mcp/elicitation.go` (+ tests).

1. **MED — unbounded server-request dispatch + Close doesn't drain.** `go c.dispatchServerRequest(req)` per request, no cap, and Close doesn't wait for in-flight dispatch goroutines (they may Send after Close). Fix: a bounded semaphore (buffered chan, cap ~8) gating dispatch + a `sync.WaitGroup` that Close drains after cancelling ctx. Over-limit: reject with `-32603` server-busy or block-with-bound (pick bounded concurrency + reject-on-full as `-32603`). Test: a burst never exceeds the cap; Close drains (no dispatch goroutine writes after Close).
2. **Functional — elicitation unreachable (protocol version pin).** Client pins `2025-03-26` and hard-rejects other versions, but elicitation is a `2025-06-18` feature → a conforming server never sends `elicitation/create`. Fix: support an ordered set {`2025-06-18`,`2025-03-26`}, send the latest in `initialize`, ACCEPT the server's returned version if it's in the set (store it); error only for versions outside the set. Advertise elicitation capability only when a handler is set (unchanged). Test: initialize sends 2025-06-18; server replying 2025-06-18 → accepted + elicitation dispatched; replying 2025-03-26 → accepted (back-compat); replying unsupported → error.
3. **LOW — elicitation handler error reported as user "cancel".** A handler bug/error is indistinguishable on the wire from a deliberate user cancel. Fix: reply with a JSON-RPC error object (`-32603`) for handler errors; reserve `cancel`/`decline` for genuine user actions (malformed params already → `-32602`). Test.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `feat(mcp): bound+drain dispatch, negotiate protocol version for elicitation, typed handler-error`

---

### Task 11: codemode — output budget covers logs, reject empty code

**Files:** `codemode/codemode.go` (+ tests).

1. **LOW — Logs bypass MaxOutputBytes.** Only `Result.Output` is truncated; `Result.Logs` appended afterward unbounded → sandboxed code that logs in a loop blows the model context despite the cap. Fix: apply the byte budget to the fully assembled output+logs string.
2. **LOW — empty `code` arg silently runs an empty program.** `Strict()` is false so a provider may emit `{}`; `code == ""` runs an empty program returning a hollow "success". Fix: return `*ai.InvalidToolArgumentsError` when `code` is empty. Test both.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `fix(codemode): apply output budget to logs, reject empty code argument`

---

### Task 12: DRY cleanup + docs + CHANGELOG

**Files:** `providers/luma/image.go`, `providers/bfl/image.go`, `providers/replicate/video.go` (dedupe local `sleep(ctx,d)` → call `internal/transcribeutil.Sleep`, the canonical impl). Docs: `docs/core/media.md` (SSRF hardening: link-local/metadata result-URL hosts rejected, body cap), `docs/mcp.md` (protocol-version negotiation, dispatch bounding, HTTP-retry idempotency caveat, tool-results-are-untrusted-model-input note), `docs/core/errors-and-retries.md` (ctx-deadline recommendation — http.DefaultClient has no timeout so callers should set ctx deadlines; streams+polls are ctx-bounded), `docs/core/tools.md` (no-arg tools now supported; empty-args normalization). `CHANGELOG.md`: new `## [Unreleased]` with `### Security` (SSRF guards, read caps, CRLF guard) + `### Fixed` (no-arg tools, gemini thought, bedrock signing, cohere count, WS conn-close, stdio write, MCP shutdown leaks + retry idempotency + version negotiation, mocks lock, retry/gauth/schema, codemode) entries for Tasks 1-11.
- Verification: snippets compile-verified; claims grepped; links resolve.

- [ ] **Step 1: Dedupe + docs + CHANGELOG; verify. Full check suite. Commit** — `chore: dedupe sleep helper, document hardening, CHANGELOG`

---

## Self-Review Notes

- Two HIGH bugs (no-arg tool calls; gemini thought→answer leak) are the headline fixes — both corrupt real output. They're in Tasks 1 and 2.
- SSRF policy: block link-local/metadata only (crown-jewel), NOT generic private ranges (self-hosted CDNs legit). Least-false-positive default; documented.
- Blanket `http.Client.Timeout` REJECTED (breaks streaming + long polls); mitigation is ctx-deadline docs + bounded reads.
- The RealtimeSession/realtimeStream ~150-line dup is NOT refactored; the concrete divergence bug is fixed directly (Task 6) with a regression test pinning all three — lower risk than a late large refactor. Generic `wsStream[E]` left as a future item.
- MCP gauth-retryable (Task 8.4) and sendMu-backoff (Task 9.9) have import-cycle / design subtleties — implementers pick the cycle-free/low-risk path or document, per the task text.
- Everything the audits marked CLEAN is out of scope.
- Order 1→12 as listed; sequential per-task review + ledger. Tasks are mostly independent (different files/packages).
