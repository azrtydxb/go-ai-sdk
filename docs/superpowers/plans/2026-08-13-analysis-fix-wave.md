# Analysis Fix Wave Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every verified-real finding from the 2026-08-13 deep-analysis verification: tool-panic recovery, middleware chaining, schema caching, MCP notifications/sampling/roots/structured-content/DELETE-termination, lifecycle callbacks for the remaining modalities, minor hardening, and StreamText+Output support.

**Architecture:** All changes are additive to the existing 3-layer design (provider interfaces → providers → ai core). No provider wire code changes. MCP client gains three opt-in handler surfaces (notifications, sampling, roots) that follow the existing elicitation pattern exactly. StreamText+Output reuses `buildOutputCall` and mirrors GenerateText's tool-mode fallback (generate_text.go:272-330).

**Tech Stack:** Go stdlib only (root module stays zero-dependency). Tests use the existing `aitest` mocks and table-driven style.

**Spec:** This plan IS the spec — it derives from the verified findings of the 2026-08-13 analysis report (verification recorded in this conversation; refuted findings P5, half of P1, and Section 6 are excluded). Prior scope rulings from docs/superpowers/specs/2026-08-03-v6-parity-final-audit.md hold.

## Global Constraints

- Root module MUST stay zero-dependency (stdlib only).
- Follow existing comment density/idiom; doc comments on every exported symbol.
- Every task: `go build ./... && go vet ./...` clean, `go test ./<touched pkg>/...` green before commit; full `go test ./...` in the final task.
- Commit messages: conventional commits, ending with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- Do NOT block private IPs in MCP transports (localhost is the dominant legitimate MCP deployment; S1/S2 are addressed as documented trust model, consistent with the v0.2.1 ruling that SSRF guards apply to server-controlled URLs only).
- Scope exclusions (documented-by-design, verified): bedrock batch size 1, SmoothStream no-default-delay, SchemaDescription native-JSON no-op, fetchmedia custom-RoundTripper pinning, Call.Headers embed/media gap, StreamObject repair-reparse cost (P8/P10/P11 — record as carried minors, do not fix here).

---

### Task 1: Tool execution panic recovery (P1)

**Files:**

- Modify: `ai/tool.go` (Execute, ~line 180)
- Test: `ai/tool_test.go`

**Interfaces:**

- Produces: no new API. `(*tool).Execute` converts a user-tool panic into `*ToolExecutionError`.

- [ ] **Step 1: Write the failing tests** in `ai/tool_test.go`:

```go
func TestExecutePanicRecovered(t *testing.T) {
	tool := NewTool("boom", "always panics", func(ctx context.Context, _ struct{}) (any, error) {
		panic("kaboom")
	})
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res != nil {
		t.Fatalf("result = %v, want nil", res)
	}
	var te *ToolExecutionError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v (%T), want *ToolExecutionError", err, err)
	}
	if te.ToolName != "boom" || !strings.Contains(te.Cause.Error(), "kaboom") {
		t.Fatalf("unexpected error contents: %v", te)
	}
}

func TestExecutePanicWithErrorValue(t *testing.T) {
	sentinel := errors.New("sentinel")
	tool := NewTool("boom2", "panics with error", func(ctx context.Context, _ struct{}) (any, error) {
		panic(sentinel)
	})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	var te *ToolExecutionError
	if !errors.As(err, &te) || !errors.Is(te.Cause, sentinel) {
		t.Fatalf("err = %v, want *ToolExecutionError wrapping sentinel", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./ai/ -run TestExecutePanic -v` — expect FAIL (panic propagates, test crashes/fails).

- [ ] **Step 3: Implement.** In `Execute`, wrap only the user-function call region (from `fnValue.Call` to the return) with a named-return recover. The panic message includes the tool name; the stack goes into the Cause so callers can log it:

```go
func (t *tool) Execute(ctx context.Context, args json.RawMessage) (result any, err error) {
	// ... existing arg decode unchanged (decode errors must NOT go through recover) ...

	// A user tool that panics must not crash the tool loop's goroutine:
	// callers (executeToolCall in generate_text.go) treat Execute as a
	// fallible call, so a panic is converted to *ToolExecutionError. A
	// panic(error) keeps its error identity via %w for errors.Is chains.
	defer func() {
		if r := recover(); r != nil {
			var cause error
			if perr, ok := r.(error); ok {
				cause = fmt.Errorf("tool panicked: %w\n%s", perr, debug.Stack())
			} else {
				cause = fmt.Errorf("tool panicked: %v\n%s", r, debug.Stack())
			}
			result = nil
			err = &ToolExecutionError{ToolName: t.name, Cause: cause}
		}
	}()

	results := fnValue.Call(...) // existing code unchanged from here down
	...
}
```

Import `runtime/debug`. Place the `defer` AFTER the arg-decoding block so decode failures keep returning `*InvalidToolArgumentsError` untouched.

- [ ] **Step 4: Run** `go test ./ai/ -run TestExecute -v` — expect PASS (including existing Execute tests).

- [ ] **Step 5: Commit** `fix(ai): recover user-tool panics into ToolExecutionError`

---

### Task 2: Minor hardening — framedTransport.Close errors.Join + RuntimeContext concurrency doc (P6, P2)

**Files:**

- Modify: `mcp/stdio.go:246-262`, `ai/runtime_context.go`
- Test: `mcp/stdio_test.go`

**Interfaces:** no new API.

- [ ] **Step 1: Write the failing test** in `mcp/stdio_test.go` — a `framedTransport` whose writer and closeFn both fail must surface both errors:

```go
type failCloser struct{ err error }
func (f failCloser) Write(p []byte) (int, error) { return len(p), nil }
func (f failCloser) Close() error                { return f.err }

func TestFramedTransportCloseJoinsErrors(t *testing.T) {
	werr := errors.New("writer close failed")
	cerr := errors.New("proc close failed")
	tr := &framedTransport{
		w:       failCloser{err: werr},
		closeFn: func() error { return cerr },
		closed:  make(chan struct{}),
		msgCh:   make(chan recvResult),
	}
	err := tr.Close()
	if !errors.Is(err, werr) || !errors.Is(err, cerr) {
		t.Fatalf("Close() = %v, want both werr and cerr via errors.Is", err)
	}
}
```

(Adjust field names/types to the actual `framedTransport` struct — check `mcp/stdio.go` for the msgCh element type and w's type; if `w` is a concrete type, add a minimal interface-compatible seam or construct via the real writer wrapped around the failCloser.)

- [ ] **Step 2: Run** `go test ./mcp/ -run TestFramedTransportCloseJoins -v` — expect FAIL (only writer error returned).

- [ ] **Step 3: Implement** — replace the if/else in `Close` with:

```go
err = errors.Join(werr, cerr)
```

(`errors.Join(nil, nil)` returns nil, so the success path is unchanged.)

- [ ] **Step 4:** In `ai/runtime_context.go`, extend the type's doc comment with the concurrency contract (no code change):

```go
// RuntimeContext is not synchronized. The tool loop executes tool calls
// sequentially, so reads and writes from tool Execute functions are safe
// without locking — but a tool that spawns its own goroutines and touches
// the map from them must provide its own synchronization.
```

- [ ] **Step 5: Run** `go test ./mcp/ ./ai/` — expect PASS. **Commit** `fix(mcp): join both close errors in framedTransport.Close; document RuntimeContext concurrency contract`

---

### Task 3: ChainMiddleware helper (F5)

**Files:**

- Modify: `ai/middleware.go` (append)
- Test: `ai/middleware_test.go`

**Interfaces:**

- Produces: `func ChainMiddleware(model provider.LanguageModel, middlewares ...func(provider.LanguageModel) provider.LanguageModel) provider.LanguageModel`

- [ ] **Step 1: Write the failing test:**

```go
func TestChainMiddlewareOrder(t *testing.T) {
	base := &aitest.MockLanguageModel{} // adjust to the mock used across middleware_test.go
	var order []string
	tag := func(name string) func(provider.LanguageModel) provider.LanguageModel {
		return func(m provider.LanguageModel) provider.LanguageModel {
			return &orderTagModel{model: m, name: name, order: &order}
		}
	}
	wrapped := ChainMiddleware(base, tag("outer"), tag("inner"))
	wrapped.Generate(context.Background(), provider.Call{})
	if len(order) != 2 || order[0] != "outer" || order[1] != "inner" {
		t.Fatalf("order = %v, want [outer inner]", order)
	}
}

func TestChainMiddlewareEmpty(t *testing.T) {
	base := &aitest.MockLanguageModel{}
	if got := ChainMiddleware(base); got != provider.LanguageModel(base) {
		t.Fatal("ChainMiddleware with no middlewares must return model unchanged")
	}
}
```

with a tiny `orderTagModel` test helper implementing `provider.LanguageModel` that appends its name in `Generate`/`Stream` then delegates. Follow the existing mock usage in `ai/middleware_test.go` (read it first; reuse its helpers).

- [ ] **Step 2: Run** — expect FAIL (undefined: ChainMiddleware).

- [ ] **Step 3: Implement:**

```go
// ChainMiddleware wraps model in middlewares, first-listed outermost:
// ChainMiddleware(m, a, b) == a(b(m)), so a sees every call first — matching
// the Vercel AI SDK's wrapLanguageModel middleware-array order. Middleware
// constructors that take extra configuration (ExtractReasoningMiddleware,
// DefaultSettingsMiddleware, TelemetryMiddleware) are adapted with a
// closure: func(m provider.LanguageModel) provider.LanguageModel {
// return ExtractReasoningMiddleware(m, opts) }. A nil middleware panics
// (programmer error, same contract as the individual constructors' nil-model
// panic); calling with no middlewares returns model unchanged.
func ChainMiddleware(model provider.LanguageModel, middlewares ...func(provider.LanguageModel) provider.LanguageModel) provider.LanguageModel {
	if model == nil {
		panic("ai: ChainMiddleware: nil model")
	}
	for i := len(middlewares) - 1; i >= 0; i-- {
		if middlewares[i] == nil {
			panic("ai: ChainMiddleware: nil middleware")
		}
		model = middlewares[i](model)
	}
	return model
}
```

- [ ] **Step 4: Run** `go test ./ai/ -run TestChainMiddleware -v` — PASS.
- [ ] **Step 5: Commit** `feat(ai): add ChainMiddleware for stacking language-model middleware`

---

### Task 4: Schema caching (P9)

**Files:**

- Modify: `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Interfaces:** no signature change; `For[T]`/`ForType` results become cached.

- [ ] **Step 1: Write the failing test:**

```go
func TestForTypeCached(t *testing.T) {
	type S struct{ A string }
	first, err := ForType(reflect.TypeOf(S{}))
	if err != nil { t.Fatal(err) }
	second, err := ForType(reflect.TypeOf(S{}))
	if err != nil { t.Fatal(err) }
	if &first[0] != &second[0] {
		t.Fatal("second ForType call must return the cached bytes (same backing array)")
	}
}

func TestForTypeErrorNotCachedAsSuccess(t *testing.T) {
	type Bad struct{ M map[int]string }
	if _, err := ForType(reflect.TypeOf(Bad{})); err == nil {
		t.Fatal("want error for int-keyed map")
	}
	if _, err := ForType(reflect.TypeOf(Bad{})); err == nil {
		t.Fatal("want error again on second call")
	}
}
```

- [ ] **Step 2: Run** — expect FAIL on the same-backing-array assertion.

- [ ] **Step 3: Implement** with a package-level `sync.Map`:

```go
// schemaCache memoizes ForType results keyed by the (pointer-normalized)
// reflect.Type. The schema for a type never changes within a process, and
// generation walks the whole struct via reflection + a full json.Marshal, so
// every GenerateObject/Output/NewTool call was paying that cost repeatedly.
// Values are cacheEntry{schema, err}; errors are cached too (they are just
// as deterministic). Callers receive the SAME json.RawMessage bytes — the
// package contract (already implicit) is that callers must not mutate the
// returned schema.
var schemaCache sync.Map // reflect.Type -> cacheEntry

type cacheEntry struct {
	schema json.RawMessage
	err    error
}
```

In `ForType`, after the pointer-deref loop and struct check, look up `schemaCache.Load(t)`; on miss compute as today, then `LoadOrStore` and return the stored entry's values (LoadOrStore, not Store, so concurrent first calls converge on one canonical value).

- [ ] **Step 4: Run** `go test ./internal/schema/ -v` — all PASS (fix TestForTypeErrorNotCachedAsSuccess if error caching interacts with the struct check ordering).

- [ ] **Step 5: Commit** `perf(schema): memoize generated schemas per reflect.Type`

---

### Task 5: MCP incoming notification handler (P3)

**Files:**

- Modify: `mcp/jsonrpc.go` (recvLoop, ~line 287), `mcp/client.go` (handler field + setter near SetElicitationHandler's pattern)
- Test: `mcp/client_test.go` (follow the existing recvLoop test harness — there are existing tests driving a fake Transport; reuse that pattern)

**Interfaces:**

- Produces: `type NotificationHandler func(method string, params json.RawMessage)`; `func (c *Client) SetNotificationHandler(h NotificationHandler)`

- [ ] **Step 1: Write the failing test:** using the package's existing fake transport, feed the client `{"jsonrpc":"2.0","method":"notifications/message","params":{"level":"info","data":"hi"}}` and assert the installed handler receives method + params; also assert that with no handler installed the message is still dropped harmlessly (no panic, client keeps serving a normal call afterward).

- [ ] **Step 2: Run** — FAIL (undefined SetNotificationHandler).

- [ ] **Step 3: Implement.** Client gains `notificationHandler NotificationHandler` guarded by `c.mu`. In recvLoop replace the bare `continue` at the idIsAbsent branch:

```go
if idIsAbsent(resp.ID) {
	// Server-initiated notification (no id → no reply expected). v1
	// dropped these; now they are handed to the installed
	// NotificationHandler, if any, on a bounded dispatch goroutine —
	// same flood protection as server-initiated requests, but since no
	// reply is owed, a saturated bound simply drops the notification
	// (best-effort delivery, matching the fire-and-forget semantics of
	// JSON-RPC notifications).
	c.mu.Lock()
	h := c.notificationHandler
	c.mu.Unlock()
	if h != nil && resp.Method != "" {
		select {
		case c.dispatchSem <- struct{}{}:
			c.dispatchWG.Add(1)
			go func() {
				defer c.dispatchWG.Done()
				defer func() { <-c.dispatchSem }()
				h(resp.Method, resp.Params)
			}()
		default:
		}
	}
	continue
}
```

`SetNotificationHandler`'s doc mirrors SetElicitationHandler: install before Initialize; handler must respect that it runs concurrently with other dispatches.

- [ ] **Step 4:** Update the recvLoop doc comment (lines 269-273) and `docs/mcp.md`'s known-gaps list (notifications entry). Run `go test ./mcp/ -v` — PASS.

- [ ] **Step 5: Commit** `feat(mcp): deliver server notifications to an installable NotificationHandler`

---

### Task 6: MCP CallTool structured content (P4)

**Files:**

- Modify: `mcp/client.go:216-264`
- Test: `mcp/client_test.go`

**Interfaces:**

- Produces:

```go
// ToolContent is one content part of a CallTool result, preserved verbatim.
type ToolContent struct {
	Type     string          // "text", "image", "audio", "resource", ...
	Text     string          // set for "text"
	Data     string          // base64 payload for "image"/"audio"
	MimeType string          // media type for binary parts
	Raw      json.RawMessage // the full wire part, for forward compatibility
}
// ToolResult gains: Content []ToolContent (Text stays as the concatenation of text parts).
```

- [ ] **Step 1: Write the failing test:** drive CallTool through the fake transport with a result containing `[{"type":"text","text":"a"},{"type":"image","data":"aGk=","mimeType":"image/png"},{"type":"text","text":"b"}]`; assert `Text == "ab"`, `len(Content) == 3`, `Content[1].MimeType == "image/png"`, `Content[1].Data == "aGk="`, and `Content[1].Raw` round-trips the original part object.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement:** extend the wire `contentPart` with `Data string`, `MimeType string` json tags, decode `Content []json.RawMessage` alongside (or re-marshal each part into Raw), build both surfaces. Update CallTool's doc comment ("other content types are ignored in v1" → preserved in Content).

- [ ] **Step 4: Run** `go test ./mcp/ -run TestCallTool -v` — PASS (existing CallTool tests must stay green).

- [ ] **Step 5: Commit** `feat(mcp): preserve non-text tool-result content in ToolResult.Content`

---

### Task 7: MCP sampling/createMessage handler (F2)

**Files:**

- Create: `mcp/sampling.go`
- Modify: `mcp/elicitation.go:71-78` (dispatch switch), `mcp/client.go` (Initialize capability declaration), `docs/mcp.md`
- Test: `mcp/sampling_test.go`

**Interfaces:**

- Produces:

```go
type SamplingMessage struct {
	Role    string          // "user" | "assistant"
	Content json.RawMessage // wire content object: {"type":"text","text":...} etc.
}
type CreateMessageRequest struct {
	Messages         []SamplingMessage
	SystemPrompt     string
	MaxTokens        int
	ModelPreferences json.RawMessage // passed through verbatim; nil if absent
}
type CreateMessageResult struct {
	Role       string          // typically "assistant"
	Content    json.RawMessage // wire content object
	Model      string
	StopReason string
}
type SamplingHandler func(ctx context.Context, req CreateMessageRequest) (CreateMessageResult, error)
func (c *Client) SetSamplingHandler(h SamplingHandler)
```

- [ ] **Step 1: Write the failing tests** (mirror `handleElicitationCreate`'s tests): (a) server sends `sampling/createMessage`, installed handler's result is marshaled back with matching id and wire keys `role`/`content`/`model`/`stopReason`; (b) no handler installed → -32601 reply; (c) malformed params → -32602; (d) handler error → -32603; (e) with a handler installed before Initialize, the initialize request's capabilities object contains a `"sampling"` key (assert on the fake transport's captured initialize params).

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** `mcp/sampling.go` following elicitation.go's structure exactly (wire structs with json tags — params keys `messages`, `systemPrompt`, `maxTokens`, `modelPreferences`; message keys `role`, `content`). Add `case "sampling/createMessage": c.handleSamplingCreateMessage(req)` to `dispatchServerRequest`. In `Initialize`, add `caps["sampling"] = struct{}{}` when a sampling handler is installed (same mu-guarded read as elicitation).

- [ ] **Step 4:** Update `docs/mcp.md:485-487` gap list (sampling now supported; describe the handler). Run `go test ./mcp/ -v` — PASS.

- [ ] **Step 5: Commit** `feat(mcp): support server-initiated sampling/createMessage via SamplingHandler`

---

### Task 8: MCP roots support (F3)

**Files:**

- Create: `mcp/roots.go`
- Modify: `mcp/elicitation.go` (dispatch switch), `mcp/client.go` (Initialize), `docs/mcp.md`
- Test: `mcp/roots_test.go`

**Interfaces:**

- Produces:

```go
// Root is a filesystem root the client exposes to the server (MCP roots
// capability). URI must be a file:// URI per the MCP spec.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}
// SetRoots installs the fixed set of roots reported to roots/list requests
// and causes Initialize to declare the "roots" capability (listChanged:
// false — v1 has no dynamic root updates). Call before Initialize.
func (c *Client) SetRoots(roots []Root)
```

- [ ] **Step 1: Write the failing tests:** (a) after `SetRoots([]Root{{URI: "file:///tmp/p", Name: "p"}})`, a server-sent `roots/list` request gets a result `{"roots":[{"uri":"file:///tmp/p","name":"p"}]}` with matching id; (b) without SetRoots, `roots/list` → -32601 (capability never declared, spec-conforming servers won't send it); (c) Initialize declares `"roots": {"listChanged": false}` when roots are set.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement** `mcp/roots.go`: mu-guarded `roots []Root` on Client (copy the slice in SetRoots), `handleRootsList` responding `respondServerResult(req.ID, rootsListResultWire{Roots: roots})`, dispatch case `"roots/list"` gated on roots having been set (unset → fall through to -32601). Initialize: `caps["roots"] = map[string]any{"listChanged": false}` when set.

- [ ] **Step 4:** Update docs/mcp.md gap list. `go test ./mcp/ -v` — PASS.

- [ ] **Step 5: Commit** `feat(mcp): advertise and serve client roots via SetRoots`

---

### Task 9: MCP HTTP DELETE session termination + transport trust-model docs (F4, S1, S2)

**Files:**

- Modify: `mcp/http.go` (Close + doc comment), `mcp/stdio.go` (NewStdioTransport doc)
- Test: `mcp/http_test.go`

**Interfaces:** no new API; `httpTransport.Close` gains best-effort DELETE.

- [ ] **Step 1: Write the failing test:** using `httptest.NewServer`: first POST responds with header `Mcp-Session-Id: sess-1` and a JSON body; then `transport.Close()`. Assert the server subsequently receives a `DELETE` request whose `Mcp-Session-Id` header is `sess-1` (capture in the handler; wait with a channel + timeout). Second test: transport that never saw a session id sends NO DELETE on Close.

- [ ] **Step 2: Run** — FAIL.

- [ ] **Step 3: Implement.** In the `closeOnce.Do` body of `Close`, before closing bodies: if the captured session id is non-empty, issue `http.NewRequestWithContext(ctx, http.MethodDelete, t.url, nil)` with the session header and auth headers, on a 5-second timeout context, via `t.client`; drain+close the response body; IGNORE the response status and any error (the spec says servers MAY respond 405 — termination is best-effort and Close's error contract is unchanged). Find the existing session-id field (grep `Mcp-Session-Id` in http.go) for the exact field/mutex names. Update the "Known deviations" doc comment (remove the DELETE bullet; keep the standalone-GET bullet).

- [ ] **Step 4: Trust-model docs (S1/S2, no code):** extend `NewStdioTransport`'s doc: "cmd is trusted developer configuration, executed verbatim; callers passing user-influenced input are responsible for validating it." Extend `NewStreamableHTTPTransport`'s doc: "url is trusted developer configuration and is not SSRF-filtered — MCP servers legitimately live on localhost/private addresses; callers exposing URL choice to untrusted input must validate it themselves."

- [ ] **Step 5: Run** `go test ./mcp/ -v` — PASS. **Commit** `feat(mcp): terminate HTTP sessions with DELETE on Close; document transport trust model`

---

### Task 10: Lifecycle callbacks for Translate/Transcribe/Speech/Image/Video (I4)

**Files:**

- Modify: `ai/translate.go`, `ai/transcribe.go`, `ai/generate_speech.go`, `ai/generate_image.go`, `ai/generate_video.go`
- Test: each file's existing `_test.go`

**Interfaces:**

- Produces, following the Embed pattern verbatim (`OnEmbedStart`/`OnEmbedEnd` at ai/embed.go:26-33 — Start fires once before the first attempt; End fires once after the final attempt with the SAME error the function returns, resp nil on error):

```go
// TranslateOpts:      OnTranslateStart  func(call provider.TranslationCall)
//                     OnTranslateEnd    func(resp *provider.TranslationResponse, err error)
// TranscribeOpts:     OnTranscribeStart func(call provider.TranscriptionCall)
//                     OnTranscribeEnd   func(resp *provider.TranscriptionResponse, err error)
// GenerateSpeechOpts: OnSpeechStart     func(call provider.SpeechCall)
//                     OnSpeechEnd       func(resp *provider.SpeechResponse, err error)
// GenerateImageOpts:  OnImageStart      func(call provider.ImageCall)
//                     OnImageEnd        func(resp *provider.ImageResponse, err error)
// GenerateVideoOpts:  OnVideoStart      func(call provider.VideoCall)
//                     OnVideoEnd        func(resp *provider.VideoResponse, err error)
```

(Check each provider call/response type name in `provider/` first — e.g. transcription may be `provider.TranscriptionCall`; use the actual names. If GenerateVideo's flow is job-based with polling, Start fires before job submission and End after the final poll resolves, with the same same-error contract.)

- [ ] **Step 1:** For ONE modality (translate), write the failing test: mock model, assert Start fires once with the built call, End fires once with the same resp; then an error-path test asserting End's err `errors.Is`-matches the returned error and resp is nil.

- [ ] **Step 2: Run** — FAIL. **Step 3:** Implement translate (mirror embed.go:54-69: build call → Start → retry.Do → translateRetryErr → End → return). **Step 4:** Run — PASS. Repeat steps 1-4 for transcribe, speech, image, video (each with both success and error tests).

- [ ] **Step 5: Run** `go test ./ai/` — PASS. **Commit** `feat(ai): lifecycle callbacks for Translate, Transcribe, GenerateSpeech, GenerateImage, GenerateVideo`

---

### Task 11: StreamText + Output (F1)

**Files:**

- Modify: `ai/stream_text.go`, `ai/output.go`, `ai/generate_text.go` (opts doc only), `ai/options.go` (OnPartialOutput)
- Test: `ai/stream_text_output_test.go` (new file)

**Interfaces:**

- Produces:
  - `StreamText` accepts `opts.Output` (drop the early `ErrOutputWithStreamText` return; keep the var with a `// Deprecated: no longer returned.` note).
  - `GenerateTextOpts.OnPartialOutput func(v any)` — during streaming with Output set, fires on each successfully parsed, distinct partial value (repair-parse of the accumulating JSON via `internal/partialjson`, dedup via `reflect.DeepEqual`, mirroring ai/stream_object.go:107-110). Never fires for OutputChoice (choices are atomic). No-op when Output is nil or in GenerateText.
  - `func (s *TextStream) Output() (any, error)` — valid after Parts() iteration completes: the decoded final value (same decode path and errors as GenerateText: `stripFences(lastText)` → `opts.Output.decode`), or an error. Suspended streams (PendingApprovals non-empty) return nil, nil like GenerateText's skip. `buildResult` populates `GenerateTextResult.Output` identically.
  - New unexported method on the `Output` interface: `decodePartial(raw string) (any, bool)` — repair+parse a partial accumulation; `ok=false` when unparseable-yet. Implemented per mode (OutputObject → T, OutputArray → []T, OutputJSON → any, OutputChoice → always false).
- Consumes: `buildOutputCall` (output.go:205), the tool-mode fallback semantics at generate_text.go:272-330, `partialjson.Repair`.

**Behavior spec (the implementer MUST read generate_text.go:260-430 and stream_text.go in full first):**

1. `StreamText` calls `buildOutputCall(opts)` instead of `buildCall(opts)`, storing `outputToolName` on the TextStream.
2. Native-JSON / schemaless mode (`outputToolName == ""`): stream parts flow through unchanged; partial accumulation taps TextDelta parts.
3. Tool-mode (`outputToolName != ""`): when a finished step's tool calls include the forced output tool — mirror generate_text.go:272-330 exactly: match by name (mismatch → the stream errors with the same `*NoObjectGeneratedError`), do NOT execute it, set the step's Text to the call's Args, scrub ToolCalls, append the synthetic RoleTool result message, map FinishToolCalls→FinishStop, and end the loop. Partial accumulation taps the forced tool call's ArgsDelta stream parts. The ToolCallDelta/ToolCallEnd parts for the forced tool are NOT yielded to the consumer (they are an encoding detail of output mode, not real tool traffic); TextDelta parts are not synthesized from them either — consumers get the final value via Output(). Note Gemini-family models deliver one full-args ToolCallDelta (I1) — accumulation must handle both delta shapes, which falls out naturally from appending deltas.
4. `OnPartialOutput`: maintain `accum []byte` + `prev any`; on each tap append, `decodePartial(string(accum))`, fire when ok && !DeepEqual(prev, v). Same pattern as stream_object.go:100-115.
5. Decode failure of the FINAL text: `Output()` returns the `*NoObjectGeneratedError`; the stream itself is NOT retroactively errored (parts already flowed).
6. Multi-step: output modes with user tools are still rejected for tool-mode by `ErrOutputRequiresJSONOrNoTools` (unchanged, from buildOutputCall). Native-JSON + user tools + streaming: decode targets the LAST step's text, same as GenerateText.

- [ ] **Step 1: Write failing tests** in `ai/stream_text_output_test.go` using the aitest mock stream helpers (read `ai/stream_text_test.go` first and reuse its harness):
  - `TestStreamTextOutputNativeJSON`: model with NativeJSON=true streaming `{"name":"bob","age":4` + `2}` as two TextDeltas; OutputObject[person]; assert parts iterate normally, OnPartialOutput fired with increasing partials (at least one partial where age==4 or name=="bob" before the final), and `s.Output()` returns person{bob,42}.
  - `TestStreamTextOutputToolMode`: model with NativeJSON=false streaming a forced tool call named `defaultSchemaName`'s args as ArgsDeltas; assert consumer sees NO tool-call parts for it, `s.Text()` equals the args JSON, FinishReason==FinishStop, Output() decodes, Messages() ends with the synthetic RoleTool message.
  - `TestStreamTextOutputWrongToolName`: forced-mode stream calling tool "other" → iteration ends with `s.Err()` being `*NoObjectGeneratedError`.
  - `TestStreamTextOutputDecodeFailure`: NativeJSON stream of non-JSON text → parts flow, `s.Output()` returns `*NoObjectGeneratedError`, `s.Err()` nil.
  - `TestStreamTextOutputChoiceNoPartials`: OutputChoice stream → OnPartialOutput never fires; Output() returns the choice.

- [ ] **Step 2: Run** — FAIL (ErrOutputWithStreamText returned).
- [ ] **Step 3: Implement** per the behavior spec. Keep the diff surgical: the tool-mode scrubbing lives where TextStream builds steps from the finished provider stream (find the step-assembly in Parts()); the partial tap lives where TextDelta/ToolCallDelta parts are observed before yielding.
- [ ] **Step 4: Run** `go test ./ai/ -v -run TestStreamText` — ALL stream tests PASS (no regressions in the existing ~suite).
- [ ] **Step 5:** Update docs: `README.md` feature table row for streaming structured output, `docs/migration guide` future-work note (grep for ErrOutputWithStreamText / "partial-output streaming" in docs/), CHANGELOG entry. **Commit** `feat(ai): StreamText structured output — Output modes, partial-output callback, tool-mode fallback`

---

### Task 12: Docs, changelog, full verification

**Files:**

- Modify: `CHANGELOG.md`, `docs/mcp.md`, `README.md`, `docs/providers/README.md` (only if it references fixed gaps)

- [ ] **Step 1:** CHANGELOG `v0.3.0` section: every task above, one line each; note the carried minors explicitly (StreamObject repair-reparse cost, EmbedMany sequential batches, Call.Headers embed/media gap).
- [ ] **Step 2:** Sweep docs for now-stale gap statements: `grep -rn "not supported\|not implemented\|future work\|ignored in v1" docs/ README.md` and fix any line a task above obsoleted.
- [ ] **Step 3:** `go build ./... && go vet ./... && go test ./...` — ALL green.
- [ ] **Step 4: Commit** `docs: v0.3.0 changelog and gap-list refresh`

---

## Self-Review Notes

- Coverage: P1✓(T1) P2✓(T2) P3✓(T5) P4✓(T6) P6✓(T2) P9✓(T4) F1✓(T11) F2✓(T7) F3✓(T8) F4✓(T9) F5✓(T3) I4✓(T10) S1/S2✓(T9 docs). Excluded by verification or design ruling: P5, P1-assertion-half, Section 6, P7, P8, P10, P11, D2, D3, S3, F6–F9, I1–I3, I5 (recorded as carried minors/by-design in T12's changelog).
- Type consistency: `ChainMiddleware`, `NotificationHandler`, `SamplingHandler`, `Root`, `ToolContent`, `OnPartialOutput`, `TextStream.Output()` — each defined once, referenced with the same name throughout.
- The F1 task depends on nothing in T1–T10 and can run in parallel with them; T5/T7/T8 share the dispatch switch and must land sequentially (T5 → T7 → T8) to avoid conflicts.
