# Model Context Protocol (MCP)

Package `mcp` implements an MCP client — talking to external tool servers
over stdio or Streamable HTTP — and adapts their tools into `ai.Tool`s so
they drop straight into `GenerateText`/`StreamText`'s `Tools` option
alongside hand-written tools.

This client speaks protocol version **`2025-03-26`**, pinned as a constant;
`Initialize` rejects the handshake outright if the server negotiates a
different version.

## Client walkthrough: stdio

The stdio transport launches a server process and speaks newline-delimited
JSON-RPC over its stdin/stdout:

```go
transport, err := mcp.NewStdioTransport(
	[]string{"my-mcp-server", "--flag"},
	[]string{"MCP_TOKEN=secret"}, // appended to the child's environment
)
if err != nil {
	log.Fatal(err)
}

client := mcp.NewClient(transport)
defer client.Close()

if err := client.Initialize(ctx); err != nil {
	log.Fatal(err)
}

tools, err := mcp.Tools(ctx, client)
if err != nil {
	log.Fatal(err)
}
```

`NewStdioTransport`'s child's stderr is passed through to the calling
process's `os.Stderr`. `client.Close` closes the child's stdin, waits
briefly for it to exit on its own, and kills it if it hasn't.

## Client walkthrough: Streamable HTTP

The Streamable HTTP transport POSTs each JSON-RPC message and reads the
response as either a single JSON body or a Server-Sent Events stream:

```go
transport := mcp.NewStreamableHTTPTransport(
	"https://mcp.example.com/mcp",
	map[string]string{"Authorization": "Bearer sk-..."},
)

client := mcp.NewClient(transport)
defer client.Close()

if err := client.Initialize(ctx); err != nil {
	log.Fatal(err)
}

tools, err := mcp.Tools(ctx, client)
```

An `Mcp-Session-Id` response header, when present, is captured and echoed
on subsequent requests automatically.

## Transports' documented deviations

Both transports are v1 implementations scoped to **tools-only** MCP
clients, and each has documented gaps against the full spec, called out
directly in their source doc comments:

**Streamable HTTP** (from `mcp/http.go`), known deviations from the full
2025-03-26 Streamable HTTP transport spec:

- `Close` does not send a `DELETE` to terminate the session on the server;
  the session, if any, is simply abandoned.
- There is no standalone `GET` request opening a server-initiated SSE
  channel, so server-initiated requests/notifications outside of a POST
  response are not supported.

It also has no persistent connection: `Receive` only ever yields data that
a prior `Send`'s response produced, matching plain JSON-RPC request/response
flow — but meaning `Receive` blocks forever if called without a
corresponding `Send` in flight. For an SSE response, `Send` hands the body
to a background drain goroutine and returns as soon as headers are read
(it doesn't block for the whole stream); this is deliberate, since `Client`
serializes all `Send`s behind one mutex and the spec only says a server
*should* (not *must*) close its SSE stream after responding — draining
synchronously would let a server that keeps the stream open wedge every
subsequent call.

**stdio** (from `mcp/stdio.go`):

- Per the MCP stdio transport spec, a JSON-RPC message must not contain an
  embedded newline (the framing is one JSON object per line); `Send`
  rejects any message that does, rather than silently corrupting the
  framing.
- Peers are expected to enforce the same (or a more generous) per-line size
  limit as this client's 10MiB `Receive` cap; a peer with a tighter line
  buffer may reject or truncate very large messages.

**Client** (`mcp/jsonrpc.go`), applying to both transports: incoming
messages that aren't responses to a call this client made (unknown id, or
no id at all — i.e. a server-initiated request or notification) are
dropped silently. Neither v1 transport nor the client supports the server
initiating its own requests.

## Tools() adapter and the tool loop

`mcp.Tools(ctx, client)` lists the server's tools (`ListTools`,
transparently paginating via `nextCursor`) and adapts each into an
`ai.Tool`: `Name`/`Description`/`Schema` pass through the server's
definition **verbatim** — the schema is not re-derived by this SDK's
reflection-based schema builder, unlike hand-written tools built with
`ai.NewTool` (see [Tools](core/tools.md)). `Execute` forwards the raw JSON
arguments straight to `CallTool`; a `ToolResult` with `IsError: true`
becomes a Go error (`*ai.ToolExecutionError`, `Cause` carrying the result's
text), so the `GenerateText`/`StreamText` tool loop records it as a failed
tool call rather than a success.

```go
tools, err := mcp.Tools(ctx, client)
if err != nil {
	log.Fatal(err)
}

result, err := ai.GenerateText(ctx, ai.GenerateTextOpts{
	Model:    openai.New().Model("gpt-4o"),
	Prompt:   "Use the available tools to answer the question.",
	Tools:    tools,
	StopWhen: ai.StepCountIs(4),
})
```

Because `mcp.Tools` returns plain `[]ai.Tool`, MCP-sourced and hand-written
tools mix freely in the same `Tools` slice, and go through the identical
multi-step loop, `RepairToolCall`, and error taxonomy described in
[Tools](core/tools.md).

## Limitations (v1)

- **Tools only.** No resources, prompts, sampling, or roots — just
  `initialize`, `tools/list`, and `tools/call`.
- **Text-content-only tool results.** `CallTool` concatenates only
  `"text"`-type content parts from the result into `ToolResult.Text`; other
  content types (e.g. images) are ignored.
- **No server-initiated traffic.** Neither transport supports the server
  calling back into the client (see the deviations above).
- **No session termination handshake** on the HTTP transport (`Close`
  simply abandons the session).

## Source of truth

- [`mcp/client.go`](../mcp/client.go) (`Initialize`, `ListTools`,
  `CallTool`, protocol version)
- [`mcp/jsonrpc.go`](../mcp/jsonrpc.go) (`Client`, `NewClient`)
- [`mcp/stdio.go`](../mcp/stdio.go) (`NewStdioTransport`,
  `framedTransport`)
- [`mcp/http.go`](../mcp/http.go) (`NewStreamableHTTPTransport`,
  `httpTransport`)
- [`mcp/tools.go`](../mcp/tools.go) (`Tools`, `mcpTool`)
- [`mcp/transport.go`](../mcp/transport.go) (`Transport` interface)

See also: [Tools](core/tools.md) for `ai.Tool`, `ai.NewTool`, and the tool
loop's error taxonomy in full.
