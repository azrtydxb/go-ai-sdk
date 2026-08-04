# Model Context Protocol (MCP)

Package `mcp` implements an MCP client — talking to external tool servers
over stdio or Streamable HTTP — and adapts their tools into `ai.Tool`s so
they drop straight into `GenerateText`/`StreamText`'s `Tools` option
alongside hand-written tools. Beyond tools, the client also supports
**resources**, **resource templates**, **prompts**, argument **completions**,
server-initiated **elicitation**, and, on the HTTP transport,
**token-provider auth with transient retry** — see the sections below.

This client offers protocol version **`2025-06-18`** (the latest it
supports) during `Initialize`, and accepts either `2025-06-18` or
`2025-03-26` back from the server; `Initialize` rejects the handshake
outright if the server negotiates anything else. `Client.ProtocolVersion()`
returns whichever of the two was actually negotiated, once `Initialize` has
succeeded. Negotiating the newer version (rather than pinning to
`2025-03-26` as an earlier revision of this client did) is what makes
[elicitation](#elicitation) reachable against a conforming `2025-06-18`
server — see the version-negotiation note in that section.

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
on subsequent requests automatically. For token-provider auth (fresh
tokens per request) and opt-in retry on transient failures, use
`NewStreamableHTTPTransportWithOptions` instead — see
[Token-provider auth and retries](#token-provider-auth-and-retries-http-transport)
below.

## Resources and resource templates

`ListResources`/`ListResourceTemplates`/`ReadResource` mirror `ListTools`'s
shape, gated on the server's `"resources"` capability (as advertised in its
`initialize` response) — calling any of the three against a server that
didn't declare `"resources"` returns a `*mcp.CapabilityError` without
sending a request:

```go
resources, err := client.ListResources(ctx)
if err != nil {
	var capErr *mcp.CapabilityError
	if errors.As(err, &capErr) {
		log.Fatalf("server does not support %s", capErr.Capability)
	}
	log.Fatal(err)
}

templates, err := client.ListResourceTemplates(ctx)

contents, err := client.ReadResource(ctx, resources[0].URI)
for _, c := range contents {
	if c.Text != "" {
		fmt.Println(c.Text)
	} else {
		fmt.Println(len(c.Blob), "bytes of", c.MimeType)
	}
}
```

- `Resource{URI, Name, Title, Description, MimeType}` and
  `ResourceTemplate{URITemplate, Name, Title, Description, MimeType}` are
  both listed with `ListResources`/`ListResourceTemplates`'s pagination
  handled transparently (repeated `resources/list`/`resources/templates/list`
  calls following the server's `nextCursor`, concatenated into one slice) —
  the exact same `paginate` helper `ListTools` already used, generalized.
- `ResourceContents{URI, MimeType, Text, Blob}`: exactly one of `Text`/`Blob`
  is set, depending on whether the server sent a `"text"` or a base64
  `"blob"` field on the wire — `ReadResource` decodes the blob for you, so
  `Blob` is always raw `[]byte`, never a base64 string.

## Prompts

`ListPrompts`/`GetPrompt` follow the same pagination and capability-gating
pattern, keyed on `"prompts"`:

```go
prompts, err := client.ListPrompts(ctx)
if err != nil {
	log.Fatal(err)
}

description, messages, err := client.GetPrompt(ctx, "summarize", map[string]string{
	"topic": "quarterly earnings",
})
if err != nil {
	log.Fatal(err)
}
for _, msg := range messages {
	for _, part := range msg.Content {
		if part.Type == "text" {
			fmt.Println(msg.Role, ":", part.Text)
		}
	}
}
```

- `Prompt{Name, Title, Description, Arguments []PromptArgument}`;
  `PromptArgument{Name, Description, Required}`.
- `GetPrompt(ctx, name, args)` returns the server's `description` alongside
  `[]PromptMessage{Role, Content []PromptPart}`.
- `PromptPart{Type, Text, Resource *ResourceContents, Data []byte,
  MimeType}` — `Type` is always set; which other field is populated depends
  on it: `"text"` → `Text`, `"resource"` → `Resource` (decoded through the
  same `ResourceContents` shape `ReadResource` uses — an embedded resource
  in a prompt message has the identical wire shape as a `resources/read`
  result), `"image"`/`"audio"` → `Data` (base64-decoded). A content-part
  `Type` this client doesn't recognize is preserved as-is (`Type` and
  `MimeType` set, every other field zero) rather than erroring, per MCP's
  forward-compatible content-type convention.
- A message's `content` field is spec'd as a single object, but is decoded
  here as **either** a single object or an array of them, always flattened
  into `[]PromptPart` — so both single-part and multi-part servers decode
  into the same Go shape.

## Completions

`Complete` requests argument-autocompletion suggestions against a prompt or
a resource template, gated on the `"completions"` capability:

```go
completion, err := client.Complete(ctx, mcp.CompletionRef{
	Type: "ref/prompt",
	Name: "summarize",
}, "topic", "quart")
if err != nil {
	log.Fatal(err)
}
fmt.Println(completion.Values, completion.Total, completion.HasMore)
```

`CompletionRef{Type, Name, URI}`: `Type` is `"ref/prompt"` (with `Name` set)
or `"ref/resource"` (with `URI` set, identifying a resource template).
`Completion{Values []string, Total int, HasMore bool}` mirrors the wire
`completion` object's `values`/`total`/`hasMore` fields verbatim.

## Elicitation

Elicitation is the one MCP extension that's **server-initiated**: the
server sends the client an `elicitation/create` request mid-session,
asking it to gather structured input from the user. Install a handler
*before* `Initialize` — the client only declares the `"elicitation"`
capability to the server when a handler is set:

```go
client.SetElicitationHandler(func(ctx context.Context, req mcp.ElicitationRequest) (mcp.ElicitationResult, error) {
	fmt.Println("server asks:", req.Message)
	// req.RequestedSchema is the JSON schema of the requested object.
	return mcp.ElicitationResult{
		Action:  "accept",
		Content: map[string]any{"confirmed": true},
	}, nil
})

if err := client.Initialize(ctx); err != nil {
	log.Fatal(err)
}
```

- `ElicitationRequest{Message, RequestedSchema json.RawMessage}`.
- `ElicitationResult{Action, Content}`: `Action` is `"accept"` (with
  `Content` set), `"decline"`, or `"cancel"`.
- **No handler installed** → the client auto-responds `Action: "decline"`
  to any `elicitation/create` it receives, and does not declare the
  `"elicitation"` capability during `Initialize` at all.
- **A handler that returns an error** → the client replies to the server
  with a JSON-RPC `-32603 Internal error`, **not** a synthesized
  `ElicitationResult{Action: "cancel"}` — a handler bug is deliberately
  distinguishable, on the wire, from a genuine user cancellation
  (`Action: "cancel"` is reserved for that case). There's no return path
  from a server-initiated request back to whatever code path is currently
  blocked on a `client.CallTool`/`ListResources`/etc. call, so a handler
  error can only be observed by the handler's own logging.
- **Any other server-initiated method** (anything besides
  `elicitation/create`) gets a JSON-RPC `-32601 Method not found` error
  response — the dispatch mechanism is generic, elicitation is just the one
  method wired up today.
- **Malformed `elicitation/create` params** (the request's `params` doesn't
  decode into `{message, requestedSchema}`) get a JSON-RPC `-32602 Invalid
  params` error response, not a synthesized `Action: "cancel"` result — a
  protocol-level shape error is distinct from the handler declining/erroring
  on a well-formed request.

> **Version-negotiation note.** Elicitation is a **2025-06-18** MCP
> feature. This client offers **`2025-06-18`** during `Initialize` and
> accepts either it or `2025-03-26` back (see the top of this page) —
> so elicitation is reachable against any spec-conforming server that
> negotiates `2025-06-18`. Against a server that negotiates the older
> `2025-03-26` (its own choice, or because it doesn't implement
> `2025-06-18`), `elicitation/create` simply isn't in that server's
> vocabulary and it won't send it — that's a property of the server, not a
> client-side restriction.

**Server-initiated request dispatch and the response-matching path.** The
client's receive loop discriminates every incoming message by shape: `id` +
no `method` is a response, matched to the pending call it answers exactly as
before; `id` + `method` is a server-initiated request, dispatched to its own
goroutine (so a slow elicitation handler can't stall reads for an unrelated
in-flight `CallTool`); no `id` is a notification, dropped as before. A
`tools/call` in flight when the server sends `elicitation/create`
concurrently is unaffected — the original call's response still matches by
`id` regardless of what other traffic interleaves on the wire.

**Dispatch is bounded.** At most 8 server-initiated requests are dispatched
concurrently (a semaphore); a 9th arriving while all 8 slots are busy gets
an immediate JSON-RPC `-32603 Internal error` ("server busy") reply instead
of queuing indefinitely, so one slow or hung handler can't back up an
unbounded number of goroutines. `Client.Close` waits (up to a 2s grace
period) for any in-flight dispatch goroutines to finish before returning,
so a handler that ignores `ctx` cancellation doesn't leak past `Close` —
after the grace period elapses, `Close` returns anyway and any remaining
goroutine is left to finish on its own, which is harmless since the
transport is already closed.

**⚠ HTTP transport cannot receive server-initiated requests.** The dispatch
machinery above is transport-agnostic, but the Streamable HTTP transport has
no server→client channel to *receive* one on — there is no standalone `GET`
opening a server-initiated SSE stream (see
[Transports' documented deviations](#transports-documented-deviations)
below), so `elicitation/create` (or any other server-initiated request) can
only reach the client over the **stdio** transport today. This is a real,
unresolved gap, not a hypothetical one — document it honestly rather than
assuming a future HTTP streaming channel closes it automatically.

## Token-provider auth and retries (HTTP transport)

`NewStreamableHTTPTransportWithOptions` is the options-taking form of the
Streamable HTTP transport, adding per-request bearer-token auth and opt-in
retry on transient failures:

```go
transport := mcp.NewStreamableHTTPTransportWithOptions(
	"https://mcp.example.com/mcp",
	mcp.WithTokenProvider(mcp.TokenProviderFunc(func(ctx context.Context) (string, error) {
		return fetchFreshToken(ctx) // called fresh on every request, and every retry attempt
	})),
	mcp.WithHTTPRetry(3), // retry up to 3 times on 429/503/connection errors
)

client := mcp.NewClient(transport)
```

- **`TokenProvider`** (`Token(ctx) (string, error)`; `TokenProviderFunc`
  adapts a plain function) supplies a token fresh on every `Send` — and on
  every retry attempt — sent as `Authorization: Bearer <token>` by default.
  `WithTokenProvider` overrides any static `Authorization` header configured
  via `NewStreamableHTTPTransport`'s legacy `headers` map.
- **`WithAuthHeader(name)`** sends the token under a custom header name (the
  raw token, no `"Bearer "` prefix) instead of `Authorization`; it has no
  effect without a `TokenProvider`.
- **`WithHTTPRetry(maxRetries)`** enables retrying `Send` on HTTP 429/503
  responses (always safe — the server either rejected or didn't process the
  request) and on a **conservative allowlist** of pre-delivery connection
  errors only: connection-refused, DNS resolution failures, and any error
  surfaced during the dial phase — all of which prove the request bytes
  never reached the server, so retrying can't double-execute a
  side-effecting call like `tools/call`. A generic `client.Do` error
  outside that allowlist (e.g. "connection reset by peer" while reading the
  response, which can happen *after* the server already processed the
  POST) is deliberately **not** retried, since the transport can't tell
  whether the server acted on the request — retrying it could execute a
  non-idempotent tool call twice. If your deployment needs broader retry
  coverage, wrap the `*http.Client` (`WithHTTPClientOpt`) with your own
  idempotency-aware retry logic instead. Backoff is capped exponential
  (`Retry-After` honored when present, either as integer seconds or an
  HTTP-date) with ctx-aware backoff waits. `maxRetries` is retries *after*
  the initial attempt — `0` (the default) disables retrying entirely.
  **Never retried:** 4xx responses other than 429, and any failure once
  response bytes (a JSON body or an SSE stream) have started being
  consumed — the at-most-once guarantee for a partially-read stream. The
  transport does not retry 401s itself; refreshing credentials on an auth
  failure is the `TokenProvider`'s own job, invoked fresh on every retry
  attempt regardless.
  **No head-of-line blocking:** the HTTP transport self-serializes
  (`SelfSerializes() bool` returns `true`), so `Client` does not hold a
  client-wide send lock across `Send` for it. One call's retry backoff (up
  to several seconds per attempt) therefore does **not** block any other
  concurrent call, or a server-initiated request reply, on the same
  `Client` — each `Send`, including its retries, runs independently. This
  is the HTTP transport's behavior specifically: the **stdio** transport's
  framing requires genuinely serialized writes, so on that transport one
  call's retry backoff still blocks every other concurrent `Send` on the
  same `Client` until it completes. If that matters for a stdio-based
  deployment, use your own idempotency-aware retry logic around
  `WithHTTPClientOpt` instead of this option — `WithHTTPRetry` itself only
  applies to the HTTP transport regardless.
  A consequence: a `TokenProvider` backing an HTTP transport used by a
  `Client` with multiple concurrent in-flight calls must support `Token`
  being called concurrently by more than one goroutine — see the
  `TokenProvider` bullet above.
- **`WithHTTPClientOpt(*http.Client)`** overrides the `*http.Client` used to
  send requests (default `http.DefaultClient`).
- `NewStreamableHTTPTransport(url, headers)` (the pre-existing constructor)
  is unchanged and still works — it now delegates to
  `NewStreamableHTTPTransportWithOptions` internally with the `headers` map
  applied as static headers, so no existing call site needs to change.

### Response body caps (HTTP transport)

Every response body the HTTP transport reads is bounded, so a compromised
or misbehaving server can't exhaust client memory by returning an unbounded
body: a non-streaming JSON-RPC success response is capped at 16 MiB; an
error response and a `202 Accepted` notification-ack (whose body is
discarded either way) are each capped at 64 KiB. Exceeding a cap fails the
call with an error rather than reading further.

## Tool results are untrusted model input

Like any tool-calling integration, the text an MCP `CallTool` result puts
into `ToolResult.Text` is not trusted or sanitized by this SDK — it's
whatever the MCP server chose to return, which the model will then see as
part of the conversation on the next turn. Treat it the same way you'd
treat any other tool output that ultimately came from a third party: don't
assume it's safe to execute, render unescaped, or use to make authorization
decisions without your own validation. This is a standard caveat for any
tool/function-calling setup (MCP or hand-written `ai.Tool`s alike), not
something specific to this client.

## Transports' documented deviations

Both transports have documented gaps against the full 2025-03-26 spec,
called out directly in their source doc comments:

**Streamable HTTP** (from `mcp/http.go`):

- `Close` does not send a `DELETE` to terminate the session on the server;
  the session, if any, is simply abandoned.
- There is no standalone `GET` request opening a server-initiated SSE
  channel, so server-initiated requests/notifications outside of a POST
  response are not supported — this is why elicitation (and any other
  server-initiated method) cannot reach the client over this transport; see
  [Elicitation](#elicitation) above.

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
- Because stdio's `framedTransport` is a genuinely bidirectional pipe, it
  **is** capable of carrying a server-initiated request/response exchange —
  this is the transport elicitation is exercised against in this SDK's own
  tests.

**Client** (`mcp/jsonrpc.go`), applying to both transports: a message is
matched to a pending call purely by having an `id` and no `method`; a
message with both an `id` and a `method` is a server-initiated request,
dispatched to `SetElicitationHandler`'s handler (or auto-declined/rejected,
see [Elicitation](#elicitation)) rather than dropped; a message with no `id`
at all (a notification) is still dropped silently, as before.

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

## Error handling

Server-side JSON-RPC errors (from `Initialize`, `ListTools`, `CallTool`,
`ReadResource`, `GetPrompt`, `Complete`, ...) come back as `*mcp.RPCError`,
which is `errors.As`-able and carries the protocol's `Code`/`Message`
fields verbatim. A call to a resources/prompts/completions method against a
server that didn't advertise the matching capability instead returns a
`*mcp.CapabilityError{Capability string}` — checked client-side, before any
request is sent. Anything else — a closed transport, a context timeout,
malformed JSON on the wire — is returned as-is, not wrapped in either type:

```go
_, err := client.CallTool(ctx, "search", args)
if err != nil {
	var rpcErr *mcp.RPCError
	var capErr *mcp.CapabilityError
	switch {
	case errors.As(err, &rpcErr):
		fmt.Printf("server error %d: %s\n", rpcErr.Code, rpcErr.Message)
	case errors.As(err, &capErr):
		fmt.Printf("server does not support %s\n", capErr.Capability)
	default:
		fmt.Println("transport error:", err)
	}
}
```

## Limitations

- **Text-content-only tool results.** `CallTool` concatenates only
  `"text"`-type content parts from the result into `ToolResult.Text`; other
  content types (e.g. images) are ignored.
- **Elicitation over HTTP is unsupported.** Server-initiated requests
  (elicitation today, and any future server-initiated method) can only
  reach the client over the stdio transport — the Streamable HTTP transport
  has no server→client channel to receive them on. See
  [Elicitation](#elicitation) and
  [Transports' documented deviations](#transports-documented-deviations).
- **No sampling or roots.** The client implements resources, prompts,
  completions, and elicitation on top of the original tools surface, but
  still has no `sampling/createMessage` or `roots/list` support.
- **No session termination handshake** on the HTTP transport (`Close`
  simply abandons the session); no `DELETE` request is ever sent.

## Source of truth

- [`mcp/client.go`](../mcp/client.go) (`Initialize`, `ListTools`,
  `CallTool`, `CapabilityError`, `hasCapability`, `paginate`,
  `supportedProtocolVersions`, `ProtocolVersion`)
- [`mcp/jsonrpc.go`](../mcp/jsonrpc.go) (`Client`, `NewClient`, `recvLoop`'s
  response/request/notification discrimination, `dispatchSem`/
  `maxConcurrentServerDispatch` bounded dispatch, `dispatchWG`/
  `closeDrainGrace` drain-on-`Close`)
- [`mcp/resources.go`](../mcp/resources.go) (`Resource`,
  `ResourceTemplate`, `ResourceContents`, `ListResources`,
  `ListResourceTemplates`, `ReadResource`)
- [`mcp/prompts.go`](../mcp/prompts.go) (`Prompt`, `PromptArgument`,
  `PromptMessage`, `PromptPart`, `ListPrompts`, `GetPrompt`)
- [`mcp/completion.go`](../mcp/completion.go) (`CompletionRef`,
  `Completion`, `Complete`)
- [`mcp/elicitation.go`](../mcp/elicitation.go) (`ElicitationRequest`,
  `ElicitationResult`, `ElicitationHandler`, `SetElicitationHandler`,
  `dispatchServerRequest`, `handleElicitationCreate`)
- [`mcp/stdio.go`](../mcp/stdio.go) (`NewStdioTransport`,
  `framedTransport`)
- [`mcp/http.go`](../mcp/http.go) (`NewStreamableHTTPTransport`,
  `NewStreamableHTTPTransportWithOptions`, `TokenProvider`,
  `WithTokenProvider`, `WithAuthHeader`, `WithHTTPRetry`,
  `WithHTTPClientOpt`, `httpTransport`)
- [`mcp/tools.go`](../mcp/tools.go) (`Tools`, `mcpTool`)
- [`mcp/transport.go`](../mcp/transport.go) (`Transport` interface)

See also: [Tools](core/tools.md) for `ai.Tool`, `ai.NewTool`, and the tool
loop's error taxonomy in full.
