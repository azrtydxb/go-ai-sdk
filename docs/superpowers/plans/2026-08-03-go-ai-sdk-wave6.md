# go-ai-sdk Wave 6 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining Vercel-AI-SDK core parity gaps: an MCP client (stdio + streamable HTTP) whose tools plug into the tool loop, telemetry hooks, stream lifecycle callbacks, tool-call repair + active-tool filtering, real FilePart support (PDF/documents) on the providers that have it, and structured provider metadata.

**Architecture:** New top-level `mcp` package (JSON-RPC 2.0 core + two transports + `Tools()` adapter returning `[]ai.Tool`). Telemetry is a middleware + a small interface (OTel-adaptable without the dependency). Loop hooks extend `GenerateTextOpts` additively. FilePart turns three providers' error paths into real document support.

**Tech Stack:** Go 1.26, stdlib only (os/exec for stdio transport).

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- Additive only for existing surfaces; new packages free.
- Existing tests stay green; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit; providertest untouched.
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: `mcp` package — JSON-RPC core + stdio transport

**Files:**
- Create: `mcp/jsonrpc.go`, `mcp/transport.go`, `mcp/stdio.go`, `mcp/client.go`
- Test: `mcp/client_test.go` (scripted in-process server over io.Pipe), `mcp/stdio_test.go` (subprocess: `go run` a tiny helper via TestMain-built binary is overkill — use an `os/exec` of `cat`-like scripted responder written as a testdata Go helper compiled with `go build` in TestMain, OR simpler: test stdio framing against an io.Pipe pair since StdioTransport should accept arbitrary io.ReadWriteCloser internally; subprocess-specific code kept thin)

**Interfaces:**
- Produces:

```go
package mcp

// Transport moves one JSON-RPC message each way. Implementations are safe
// for one concurrent reader and one concurrent writer.
type Transport interface {
    Send(ctx context.Context, msg json.RawMessage) error
    Receive(ctx context.Context) (json.RawMessage, error)
    Close() error
}

// NewStdioTransport launches cmd (argv form) and speaks newline-delimited
// JSON-RPC over its stdin/stdout (MCP stdio framing: one JSON object per
// line). Env entries are appended to the child's environment.
func NewStdioTransport(cmd []string, env []string) (Transport, error)

type Client struct{ /* unexported */ }
// NewClient wraps a transport. Initialize performs the MCP handshake:
// initialize request (protocolVersion "2025-03-26", clientInfo
// {name:"go-ai-sdk", version:"0.1"}, capabilities{}) → response →
// notifications/initialized notification.
func NewClient(t Transport) *Client
func (c *Client) Initialize(ctx context.Context) error

type ToolDef struct {
    Name        string
    Description string
    InputSchema json.RawMessage
}
// ListTools issues tools/list (paginating via nextCursor until exhausted).
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error)

type ToolResult struct {
    Text    string // concatenated text content parts
    IsError bool
}
// CallTool issues tools/call {name, arguments}. Content parts of type
// "text" are concatenated; other content types are ignored in v1 (doc note).
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error)
func (c *Client) Close() error
```

JSON-RPC core (`jsonrpc.go`, unexported): request ids are monotonically increasing ints; a single receive loop (started by NewClient) dispatches responses to waiting calls by id (map[int64]chan, mutex); server-initiated requests/notifications other than expected ones are ignored (logged nowhere — dropped silently, doc note). Errors: JSON-RPC error object → `*RPCError{Code int, Message string}` (exported, errors.As-able). Context cancellation abandons the wait (the id's channel is removed).

Tests: scripted server over an in-process Transport (channel-backed test transport): handshake sequence, ListTools pagination (2 pages), CallTool text concatenation + isError, RPCError surfacing, ctx cancellation mid-call, Close unblocks pending Receive. Stdio framing test: transport over a pipe with a fake server goroutine writing newline-delimited messages.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat(mcp): JSON-RPC client with stdio transport`

---

### Task 2: `mcp` streamable HTTP transport + `Tools()` ai adapter

**Files:**
- Create: `mcp/http.go`, `mcp/tools.go`
- Test: `mcp/http_test.go` (httptest server), `mcp/tools_test.go`

**Interfaces:**
- Produces:

```go
// NewStreamableHTTPTransport speaks the MCP Streamable HTTP transport:
// each Send POSTs the JSON-RPC message to url (Content-Type application/json,
// Accept "application/json, text/event-stream"); responses arrive either as
// a direct application/json body or as an SSE stream (text/event-stream)
// whose events carry JSON-RPC messages — both are fed to Receive in order.
// The Mcp-Session-Id response header, when present, is echoed on subsequent
// requests. headers are added to every request (e.g. Authorization).
func NewStreamableHTTPTransport(url string, headers map[string]string) Transport

// Tools lists the server's tools and adapts each to an ai.Tool whose
// Execute calls CallTool; a ToolResult with IsError=true becomes a Go error
// (so the ai loop records it as a failed tool call). Name/Description/
// schema pass through verbatim (schema NOT re-derived).
func Tools(ctx context.Context, c *Client) ([]ai.Tool, error)
```

Note: `ai.Tool` is an interface (Name/Description/Schema/Execute) — `mcp.Tools` returns adapter values implementing it; `ai.NewTool` is NOT used (schema comes from the server). The adapter's Execute unmarshals nothing — args pass through as raw JSON.

Tests: httptest server covering direct-JSON response, SSE response (two messages in one stream), session-id echo, extra headers; Tools adapter: schema passthrough byte-identical, Execute happy path, IsError→error, integration with ai.GenerateText tool loop (mock LanguageModel requests the MCP tool; loop executes it via a scripted MCP server).

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat(mcp): streamable HTTP transport and ai.Tool adapter`

---

### Task 3: Telemetry middleware + stream lifecycle callbacks

**Files:**
- Create: `ai/telemetry.go`
- Modify: `ai/options.go`, `ai/stream_text.go`, `ai/generate_text.go`
- Test: `ai/telemetry_test.go`, additions to `ai/stream_text_test.go`

**Interfaces:**
- Produces:

```go
// ai/telemetry.go
type SpanInfo struct {
    Operation    string        // "generate" | "stream"
    ModelID      string
    ProviderName string
    StartTime    time.Time
    EndTime      time.Time     // zero on Start
    Usage        provider.Usage // zero on Start
    FinishReason provider.FinishReason
    Err          error
}
// Telemetry receives span events. Implementations must be safe for
// concurrent use. Adapt to OTel by implementing this with a tracer.
type Telemetry interface {
    OnSpanStart(info SpanInfo)
    OnSpanEnd(info SpanInfo)
}
// TelemetryMiddleware wraps a model: Generate emits one span; Stream emits
// a span ending when the stream's FinishPart is observed (or Err/Close).
func TelemetryMiddleware(model provider.LanguageModel, t Telemetry) provider.LanguageModel

// GenerateTextOpts additions (stream-oriented lifecycle, both honored by
// StreamText; OnFinish also by GenerateText):
OnChunk  func(part provider.StreamPart) // each part before it is yielded (StreamText only)
OnFinish func(result *GenerateTextResult) // after the loop completes successfully; for StreamText fires at natural stream end with the accumulated result
OnError  func(err error) // terminal error (start failure handled by the returned error, so: mid-stream errors + tool-loop errors)
```

Tests: middleware Generate span (usage/finish populated, Err on failure); middleware Stream span timing (end at FinishPart; end-with-Err on truncation); OnChunk sees every part in order; OnFinish equivalence between GenerateText and a fully-consumed StreamText on the same mock script; OnError on mid-stream error.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: telemetry middleware and stream lifecycle callbacks`

---

### Task 4: RepairToolCall + ActiveTools

**Files:**
- Modify: `ai/options.go`, `ai/generate_text.go`, `ai/stream_text.go`
- Test: `ai/tool_loop_test.go`, `ai/stream_text_test.go` additions

**Interfaces:**
- Produces (GenerateTextOpts fields, both loops):

```go
// ActiveTools, when non-nil, limits which of Tools are OFFERED to the model
// (ToolDefs in the Call) — execution still resolves against the full Tools
// list (a repaired or hallucinated call to an inactive tool follows normal
// unknown-tool rules against the ACTIVE set: calling an inactive tool is
// NoSuchToolError).
ActiveTools []string
// RepairToolCall is invoked when a tool call fails to validate — unknown
// tool name, or InvalidToolArgumentsError from Execute. It may return a
// corrected call (retried ONCE per original call) or false to give up
// (original error semantics then apply).
RepairToolCall func(ctx context.Context, call ToolCallRecord, toolErr error) (ToolCallRecord, bool)
```

Semantics detail: repair applies before the unknown-tool abort and before recording an InvalidToolArguments failure; the repaired call re-runs validation+execution once; a second failure follows the normal path (NoSuchToolError abort / recorded tool error). Wire this into `runToolCalls` (shared by both loops) via an optional repair func parameter — adjust its signature (internal, unexported — allowed).

Tests: ActiveTools filters Call.Tools (recorded call assertions) while full-list execution works for active ones; inactive tool call → NoSuchToolError; repair fixes an unknown-name call (model calls "get_wether", repair corrects to "get_weather", loop proceeds); repair fixes bad args; repair returning false → original error; repair loop cap (repaired call failing again does NOT re-invoke repair). Both loops.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: RepairToolCall and ActiveTools loop options`

---

### Task 5: FilePart support (anthropic, geminicompat, openaicompat)

**Files:**
- Modify: `providers/anthropic/wire.go`, `internal/geminicompat/wire.go`, `internal/openaicompat/wire.go` (+ their tests)
- Modify: `provider/message.go` (FilePart doc comment update: supported by anthropic/google/vertex/openai-compatible for PDFs; other providers error)

**Wire mappings (user-message file parts only; assistant-message FilePart remains an error everywhere):**
- **anthropic**: FilePart{MediaType:"application/pdf"} → content block `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":<b64>}}`; any other MediaType → existing descriptive error.
- **geminicompat** (google + vertex): FilePart (any MediaType) → `{"inlineData":{"mimeType":<MediaType>,"data":<b64>}}` (Gemini accepts PDFs, audio, video inline).
- **openaicompat**: FilePart{MediaType:"application/pdf"} → content part `{"type":"file","file":{"filename":<Filename or "file.pdf">,"file_data":"data:application/pdf;base64,<b64>"}}` (OpenAI chat-completions file part shape); other MediaTypes → existing error. Presets inherit; note in doc comment that only OpenAI itself is known to accept it (compat servers may reject — passthrough is correct behavior).

Tests per converter: request-shape assertions for the PDF case; non-PDF error preserved where applicable (anthropic, openaicompat); gemini non-PDF (e.g. audio/mpeg) accepted.

- [ ] **Step 1: Failing tests → implement → pass. Full check suite. Commit** — `feat: FilePart document support for anthropic, gemini, openai`

---

### Task 6: ProviderMetadata + docs

**Files:**
- Modify: `provider/response.go` (`Response.ProviderMetadata map[string]any` — nil when none), `providers/anthropic` (populate `{"anthropic": {"cache_creation_input_tokens": n}}` when non-zero), `internal/openaicompat` (populate `{"<name>": {"system_fingerprint": s}}` when present)
- Modify: `README.md`, `docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md`
- Test: per-provider metadata tests

**Docs:** README "MCP" section (client, both transports, Tools adapter, tool-loop example), telemetry + lifecycle callbacks, repair/ActiveTools, FilePart support matrix, ProviderMetadata. Spec: "## Wave 6 (shipped)" section. Verify claims against code; table cell counts. Also add `examples/mcp-tools/main.go` (~35 lines: stdio MCP server command from os.Args, list tools, run one GenerateText turn with them; env-guarded).

- [ ] **Step 1: Failing metadata tests → implement → pass. Step 2: docs + example (compiles). Full check suite. Commit** — `feat: provider metadata; docs: wave 6`

---

## Self-Review Notes

- **Parity ledger:** MCP client (T1–T2) ≈ Vercel experimental MCP client (stdio + streamable HTTP, tools()); telemetry (T3) is the OTel-free analog of experimental_telemetry (documented divergence); onChunk/onFinish/onError (T3); repairToolCall + activeTools (T4); file parts (T5); providerMetadata (T6, minimal). Remaining after wave 6: anthropic citations, provider-executed tools, OTel-native exporter — documented as out of scope in the spec section.
- **Type consistency:** mcp.Client/ToolDef/ToolResult produced T1, consumed T2; Telemetry/SpanInfo self-contained T3; ToolCallRecord already exists in ai (T4 reuses); FilePart already exists in provider (T5 implements).
- **Import direction:** mcp imports ai (for ai.Tool) — no cycle (ai does not import mcp).
