# go-ai-sdk Follow-ups Plan (close every deferred/documented-not-fixed item)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Fix every item that prior audits/reviews flagged but I documented or deferred rather than fixed — the non-blocking follow-ups plus the "left out" minors. The ONLY excluded item is live-API smoke testing (still blocked on keys). Targets v0.2.2.

**Source:** the v0.2.1 hardening final whole-branch review's 3 non-blocking minors + the documented-not-fixed items from the security/concurrency/correctness audits and per-task reviews.

## Global Constraints

- Root module stays **zero-dependency** (stdlib only). contrib/otel untouched.
- Providers never retry; existing conventions + all v0.2.0/v0.2.1 behavior preserved (these are refinements, not redesigns of working contracts — regressions are the top risk).
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` (+`-race` on ai/mcp/internal/websocket/the WS providers/fetchmedia when touched).
- Commits conventional, trailer: `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: MCP sendMu contention — don't hold it across HTTP retry/Send; bound the busy-reply acquisition

**Files:** `mcp/jsonrpc.go`, `mcp/http.go`, `mcp/transport.go` (+ tests).

**Problems:**
- `Client.call` holds `sendMu` for the entire `transport.Send`, which for the HTTP transport spans the POST plus (with WithHTTPRetry) up to maxRetries backoff sleeps (≤10s each) — head-of-line-blocking every other concurrent call and every server-request reply on the same Client (T9 M-9, kept+documented).
- The saturated-dispatch "server busy" reply (`respondServerError` from recvLoop) acquires `sendMu` unboundedly; if a stalled stdio write holds `sendMu`, recvLoop blocks on the acquire despite the 200ms send-timeout that only bounds the write itself (final-review #1).

**Why sendMu exists:** it serializes writes. The **stdio** framed transport NEEDS this (interleaved writes corrupt newline framing). The **HTTP** transport does NOT — each Send is an independent POST and httpTransport is already internally synchronized (its own mu/recvCh); concurrent POSTs are fine (responses correlate by JSON-RPC id in recvLoop).

**Fix:**
1. Add an optional transport capability so the Client knows whether it must serialize Sends:
```go
// In transport.go: a transport that serializes its own concurrent Sends
// (so the Client need not hold sendMu across Send) implements this.
type selfSerializingTransport interface {
	// SelfSerializes reports that concurrent Send calls are safe without
	// external serialization. The HTTP transport returns true; the stdio
	// framed transport does not implement this (needs sendMu for framing).
	SelfSerializes() bool
}
```
   httpTransport implements `SelfSerializes() bool { return true }`. framedTransport does not (or returns false).
2. In `Client.call` (and any other Send site — notify, respondServer*): if the transport self-serializes, do NOT hold `sendMu` across `transport.Send`; if it doesn't, keep holding it as today. Concretely: gate the `sendMu.Lock()/Unlock()` around Send on `!selfSerializes`. Determine self-serialization once at NewClient (store a bool on Client) to avoid a type-assert per call.
3. Busy-reply bound: `respondServerError`/`sendServerResponse` on the saturated-dispatch path must not let a stuck `sendMu` wedge recvLoop. Since after fix (2) the HTTP transport won't hold sendMu at all, the wedge only remains for stdio — and a stuck stdio write already means the client is dead. Still, make the busy-reply's `sendMu` acquisition bounded: try to acquire with a short timeout (a `sendMu` that's a `chan struct{}{}`-based trylock, or select on a timer); if it can't acquire in ~200ms, drop the busy-reply (the server times out its own request). Document.

**Tests:** with a mock/http transport, N concurrent calls where one is slow-retrying (429s with a Retry-After) do NOT serialize behind it (assert concurrency — the others complete while the slow one backs off); stdio still serializes (framing intact — the existing stdio tests must stay green); a saturated-dispatch busy-reply doesn't block recvLoop when sendMu is held (bounded). -race -count=5 on mcp.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `fix(mcp): don't hold sendMu across self-serializing HTTP Send; bound busy-reply acquire`

---

### Task 2: fetchmedia — reuse the pinned transport + multi-IP failover

**Files:** `internal/fetchmedia/fetchmedia.go` (+ test).

**Problems:**
- Every `Fetch` calls `PinnedTransport(client.Transport)` → `http.Transport.Clone()`, so no connection reuse across media fetches; each clone's idle conns linger to IdleConnTimeout → socket churn under burst image/video generation (final-review #2).
- The pinned DialContext dials only the FIRST vetted IP; a host whose first resolved record is dead now fails where v0.2.0 would try the next (final-review #3).

**Fix:**
1. Cache the pinned transport keyed by the underlying base RoundTripper, so repeated Fetch calls with the same caller `*http.Client` reuse ONE pinned transport (and thus its connection pool). Use a `sync.Map` (or a small mutex+map) keyed by the base `http.RoundTripper` (the caller's `client.Transport`, or a sentinel for nil→DefaultTransport). Return the cached wrapped transport. This is transparent — same behavior, connection reuse restored. (Cache is process-lifetime; entries are few — one per distinct provider client — so no eviction needed; document.)
2. Multi-IP failover in the pinned DialContext: vet ALL resolved IPs (reject if ANY is blocked — keep that), then try dialing them in resolved order, returning the first successful connection; only fail if all vetted IPs fail. Every dialed IP is still a vetted literal (rebind-safe). Preserve the existing "reject if any resolved IP is blocked" semantics (do NOT dial a partially-safe set — if any IP is blocked the host is rejected, matching current behavior).

**Tests:** two Fetch calls with the same client reuse the same pinned transport (assert the cache returns the same instance, or observe connection reuse via a counting dialer); a host resolving to [dead-vetted-IP, good-vetted-IP] succeeds by failing over to the second (fake resolver + a dialer that refuses the first IP); a host with any blocked IP is still rejected pre-dial (unchanged); the DNS-rebind pin still holds (existing tests green). -race.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `perf/fix(fetchmedia): reuse pinned transport across fetches; fail over across vetted IPs`

---

### Task 3: internal/websocket — stop a queued writer from clobbering an in-flight control write

**Files:** `internal/websocket/websocket.go` (+ test).

**Problem (T6 carried-minor, pre-existing):** a blocked `WriteText`/`WriteBinary` starts its ctx-deadline watcher BEFORE acquiring `writeMu`. If its ctx fires while it's still waiting on `writeMu` (because Read's auto-pong currently holds `writeMu`), the watcher calls `SetWriteDeadline(past)` on the shared conn, aborting the healthy, in-budget auto-pong write that holds the lock. Cross-call clobbering under the documented one-writer+one-reader model.

**Fix:** arm the ctx deadline-watcher only while the operation actually owns the conn for I/O — i.e., start `runWithContext`'s deadline management AFTER `writeMu` is acquired, not during the mutex wait. Read `writeMessage`/`writeControl`/`runWithContext` and restructure so the deadline (and its watcher) is scoped to the actual `conn.Write`/`conn.Read` call, not the lock-acquisition wait. A writer blocked on `writeMu` whose ctx fires should simply return ctx.Err() WITHOUT touching the conn deadline (it hasn't started writing; nothing to interrupt), leaving the in-flight control write untouched. The reader path (Read) similarly should only arm its read-deadline watcher around the actual read, and its auto-pong control write should arm a write-deadline only for that write. Preserve: normal ctx-cancel of a genuinely-in-flight Read/Write still interrupts it; the bounded control-frame write deadline from v0.2.1 still applies; direction-scoping intact.

**Tests:** a writer blocked on writeMu whose ctx cancels while a control write holds the lock → the control write completes successfully (not aborted), the blocked writer returns ctx.Err(); normal in-flight Read/Write ctx-cancel still interrupts; existing websocket tests green under -race -count=10 (this is subtle timing — stress it).

- [ ] **Step 1: Failing test (reproduce the clobber) → implement → green. Full check suite (-race -count=10 on internal/websocket). Commit** — `fix(internal/websocket): scope ctx write-deadline to the actual write, not the mutex wait`

---

### Task 4: Extract the shared WebSocket-stream machinery (deepgram live / OpenAI realtime transcription / OpenAI realtime voice)

**Files:** Create `internal/wsstream/wsstream.go` (+ test). Refactor `providers/deepgram/live.go`, `providers/openai/realtime_transcription.go`, `providers/openai/realtime.go` to use it.

**Problem:** the three WS stream implementations duplicate ~150 lines of dial/scheme-swap, struct fields (conn/events/err/closed/closeCh/readLoopDone/writeMu), Close/isClosed/Err/setErr/Events, and the readLoop skeleton (incl. the `defer conn.Close` teardown and the closeCh-in-events-send select). This duplication already PRODUCED a real bug (the missing conn-close defer in RealtimeSession, fixed in the hardening). Extract it so the teardown/leak-safety lives in ONE place.

**Design:** a generic (Go 1.26 generics) `internal/wsstream`:
```go
package wsstream

// Stream is a live bidirectional WS session yielding decoded events of type E.
type Stream[E any] struct { /* conn, events chan E, err, mu, closeCh, readLoopDone, writeMu ... */ }

// Config drives one Stream.
type Config[E any] struct {
	Conn *websocket.Conn
	// Decode parses one incoming WS message into zero or more events plus a
	// terminal flag (a clean end without an error, e.g. Deepgram Metadata /
	// OpenAI terminal event). A non-nil error ends the stream with that error.
	Decode func(messageType int, data []byte) (events []E, terminal bool, err error)
}

func New[E any](cfg Config[E]) *Stream[E]   // starts the readLoop goroutine
func (s *Stream[E]) Events() iter.Seq[E]     // single use
func (s *Stream[E]) Err() error
func (s *Stream[E]) Send(ctx context.Context, mt int, data []byte) error // writeMu-serialized
func (s *Stream[E]) Close() error            // idempotent; closeCh + conn close
```
The readLoop MUST carry the `defer conn.Close(...)` teardown and the closeCh-in-the-events-send select (the leak-safety the hardening added). Each provider supplies its own Decode + its provider-specific Send framing (base64 append for realtime, binary for deepgram, CloseStream/commit control messages) and keeps its provider-specific surface (TranscriptionStream/RealtimeSession method sets) as a thin wrapper over the generic Stream. Providers keep their dial + scheme-swap OR move a shared scheme-swap helper into wsstream (a `DialURL(baseURL, path)` deriving ws(s):// — dedupe that too if clean).

**Preserve EXACTLY:** all three providers' external behavior (method sets, event shapes, Err semantics, closeSendSent guards, the conn-close-on-readLoop-exit, control-frame framing). The existing regression tests (conn-close in all 3, leak tests, event-sequence tests, -race) are the safety net — they must ALL stay green unchanged. This is a refactor with zero behavior change.

**Tests:** wsstream unit tests (Decode routing, terminal flag, error end, Send serialization, Close idempotency, abandoned-Events + Close reclaims the reader — the leak test, at the generic level); all THREE providers' existing test suites pass unchanged under -race; a conn-close regression test still holds per provider (already exist).

- [ ] **Step 1: wsstream + tests → green. Commit** — `refactor(internal/wsstream): shared WS-stream machinery`
- [ ] **Step 2: migrate the 3 providers → their suites green unchanged (-race). Full check suite. Commit** — `refactor: deepgram/openai WS providers use internal/wsstream`

---

### Task 5: Smaller leftovers — gauth token_uri validation, retry.BaseDelay safety, websockettest length guard, MCP string-id test

**Files:** `internal/gauth/gauth.go`, `internal/retry/retry.go`, `internal/websocket/websockettest/websockettest.go`, `mcp/*_test.go` (+ tests).

1. **gauth token_uri validation** (security S9): the token endpoint URL comes from the service-account key JSON and becomes both the POST target and the JWT `aud`. Validate it before use: require an absolute `https://` URL (reject non-https / relative / empty) with a clear error. This is standard-Google-compatible (real keys carry an https token_uri) while blocking a tampered key from redirecting the signed assertion to an arbitrary/plaintext host. Test: a key with an http:// or relative token_uri → error; a normal https one works.
2. **retry.BaseDelay** (concurrency finding 7): it's an exported mutable package global read by calculateBackoff — a data race if mutated concurrently with an in-flight retry. It's a test-tuning knob. Fix minimally without an API-churning signature change: keep it but route reads through a function and document it as test-only / set-before-use / not-for-concurrent-mutation; OR (cleaner) make it unexported and add an exported test-only setter `SetBaseDelayForTest(d) (restore func())` mirroring the fetchmedia resolver seam, and update the (test) callers. Prefer the setter approach — removes the mutable-global-read-in-production concern. Verify all callers (tests) updated.
3. **websockettest.ReadMessage** (roadmap carried-minor, test-only): it `make([]byte, length)` on an unvalidated declared frame length — a misbehaving client fixture could OOM a test server. Cap the declared length (e.g. reject > a few MiB) before allocating. Test-helper hardening; keep it simple.
4. **MCP string-id interop** (roadmap note): add a focused test that a server-initiated request with a STRING JSON-RPC id round-trips (the id is echoed verbatim in the response) — pins the json.RawMessage id handling that landed in wave 13. (If such a test already exists from wave 13, verify + note; else add.)

**Tests:** per item above. Full check suite.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on internal/retry + mcp). Commit** — `fix(internal): validate gauth token_uri, guard retry BaseDelay, cap websockettest length, pin MCP string-id`

---

### Task 6: Docs + CHANGELOG + release v0.2.2

**Files:** `CHANGELOG.md` (new `## [0.2.2]` under [Unreleased] — Security/Fixed/Changed/Performance for Tasks 1-5), `docs/mcp.md` (sendMu-no-longer-HOL for HTTP; the WithHTTPRetry note updated), `docs/core/media.md`/`docs/providers/README.md` (fetchmedia connection reuse + failover — brief), `docs/architecture.md` (mention internal/wsstream if internals are listed), any doc referencing the WS providers' internals. Verify snippets/claims/links.
- Do NOT re-tag contrib/otel (unchanged; root v0.2.2 is backward-compatible for it).

- [ ] **Step 1: docs + CHANGELOG; verify. Full check suite. Commit** — `docs: follow-ups — CHANGELOG v0.2.2, MCP/media/wsstream notes`

---

## Self-Review Notes

- Task 4 (WS dedup) is the highest-regression-risk — it refactors three WORKING providers. The rule: zero behavior change; the existing regression/leak/-race tests are the gate; if any provider test needs modifying to pass, that's a signal the refactor changed behavior — stop and reconcile.
- Task 1 (sendMu) changes MCP client concurrency — the stdio-still-serializes invariant is load-bearing (framing); test it explicitly.
- Task 3 (WS deadline) is subtle timing — stress with -count=10.
- Everything the audits marked CLEAN stays untouched. This pass ONLY closes the documented-not-fixed items; it introduces no new features.
- Order 1→2→3→4→5→6. Sequential per-task review + ledger.
