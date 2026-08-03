# Troubleshooting

Common failure modes across providers, structured output, streaming, tool
calling, and MCP, with pointers to the deeper docs for each.

## Authentication failures

Every provider reads its API key from an environment variable by default
and accepts a `With*` functional option to override it — see
[Getting started](getting-started.md#environment-variables) for the full
env var table. A few providers need more than a bare API key:

- **Azure OpenAI** — needs both `AZURE_API_KEY` and either
  `AZURE_RESOURCE_NAME` (`azure.WithResourceName`) or an explicit base URL
  (`azure.WithBaseURL`). `WithBaseURL` always wins over
  `WithResourceName`/`AZURE_RESOURCE_NAME` when both are set — if requests
  are going to the wrong host, check for a stray `WithBaseURL`. See
  [Azure](providers/azure.md).
- **Vertex AI** — needs a project and location
  (`GOOGLE_VERTEX_PROJECT`/`vertex.WithProject`,
  `GOOGLE_VERTEX_LOCATION`/`vertex.WithLocation`, defaulting to
  `us-central1`) plus credentials — Vertex has no API-key concept, only an
  OAuth2 bearer token. If no token source is configured (neither
  `vertex.WithTokenSource`/`vertex.WithAccessToken`), `New()` falls back to
  `GOOGLE_APPLICATION_CREDENTIALS` — but this is **not** the full Google
  Application Default Credentials
  chain (no metadata-server lookup, no `gcloud auth` fallback), so running
  under GCE/GKE workload identity without an explicit credentials file will
  fail. Also note the project ID is only required at request time, not at
  construction time — `New()` can succeed and the first call still fail
  with a missing-project error. See [Vertex AI](providers/vertex.md).
- **Amazon Bedrock** — there is no API key; every request is signed with
  AWS Signature Version 4 using `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/
  `AWS_SESSION_TOKEN` (or `bedrock.WithCredentials`) and a region
  (`AWS_REGION`, else `AWS_DEFAULT_REGION`, else `us-east-1`). This is
  **not** the full AWS SDK default credential chain (no shared config file,
  no SSO, no instance-role lookup) — if you need those, resolve credentials
  yourself with the AWS SDK for Go and pass the result explicitly. See
  [Bedrock](providers/bedrock.md).

- **fal, Replicate, Luma, Deepgram, LMNT, Hume** — each of these six media
  providers uses a different, provider-specific auth header rather than a
  uniform `Authorization: Bearer`: fal sends `Authorization: Key <key>`,
  Deepgram sends `Authorization: Token <key>`, LMNT sends `X-API-Key`,
  and Hume sends `X-Hume-Api-Key`; only Replicate and Luma use the
  standard `Authorization: Bearer <key>` shape. If one of these providers
  returns a 401/403, double-check you're not assuming the `Bearer` shape
  applies — see each provider's own page (linked from
  [Provider overview](providers/README.md#construction-at-a-glance)) for
  its exact header and env var. fal is also the one provider in this SDK
  with a two-variable fallback: `WithAPIKey` defaults to
  `FAL_API_KEY`, then `FAL_KEY` if that's unset.
- **AssemblyAI, Gladia, Rev.ai** — three more provider-specific auth
  headers: AssemblyAI sends `authorization: <key>` (lowercase header name,
  and — unlike almost every other provider in this SDK — no `Bearer`
  prefix on the value), Gladia sends `x-gladia-key`, and Rev.ai uses the
  standard `Authorization: Bearer <key>` shape. Rev.ai also has its own
  two-variable fallback: `WithAPIKey` defaults to `REVAI_API_KEY`, then
  `REV_AI_API_KEY` if that's unset. See
  [AssemblyAI](providers/assemblyai.md), [Gladia](providers/gladia.md), and
  [Rev.ai](providers/revai.md).

If a request fails with an auth-shaped error, it surfaces as
`*ai.APICallError` — check `StatusCode` and the provider's raw response
body (see [Errors and retries](core/errors-and-retries.md#apicallerror)).

## NoObjectGeneratedError (structured output)

`GenerateObject`/`StreamObject` return `*ai.NoObjectGeneratedError` (with
`RawText` and `Cause`) whenever the model's output couldn't become a valid
`T`: JSON decoding failed, or — in tool mode — the model never called the
injected tool. `errors.As` into it and print `RawText` first; it's almost
always more informative than the wrapped `Cause`:

```go
var noObj *ai.NoObjectGeneratedError
if errors.As(err, &noObj) {
	log.Fatalf("model didn't produce a valid object: %v (raw: %s)", noObj.Cause, noObj.RawText)
}
```

Before assuming a prompt problem, check which mode the provider actually
uses — native JSON-schema mode vs. a forced tool call — since the failure
shape differs:

- **Tool-mode providers** (`Capabilities().NativeJSON == false`) —
  Anthropic and Bedrock — never send your schema in a JSON-response field;
  they inject a forced tool call instead. A `NoObjectGeneratedError` here
  usually means the model didn't call the injected tool at all.
- **DeepSeek** sends only `{"type":"json_object"}` on the wire (its
  `JSONObjectOnly` option) — it rejects OpenAI's schema-bearing
  `json_schema` shape outright — so schema conformance is enforced
  entirely by `GenerateObject`'s own decode step, not by DeepSeek.
- **Mistral** has no `json_schema` mode at all: it always sends
  `{"type":"json_object"}` and ignores `Schema` completely, same
  decode-step-only enforcement as DeepSeek.

See [Structured output](core/structured-output.md#noobjectgeneratederror)
for the full per-provider `NativeJSON` table.

## Streaming issues

- **A stream ends with no `FinishPart` and no visible error until you check
  `Err()`.** `stream.Err()` is `nil` until iteration ends; always check it
  after the `for range stream.Parts()` loop, not just inside it — a
  mid-stream provider error, an unresolved `*ai.NoSuchToolError`, or a
  `*ai.RetryError` from a failed later step all surface only through
  `Err()` after the loop exits. See
  [Streaming: the iterator, Err(), and Close()](core/streaming.md#the-iterator-err-and-close).
- **A stream appears to cut off early, with no error at all.** Some
  proxies drop the trailing SSE terminator (`data: [DONE]`) after
  forwarding the real payload. The SDK's truncation rule handles this: if
  a finish-reason-bearing chunk was seen before the connection closed, the
  stream is still treated as well-formed and yields its single
  `FinishPart`; only a connection that closes *before* any finish-reason
  chunk arrives is treated as truly truncated (zero `FinishPart`s, `Err()`
  set). If you're behind a buffering reverse proxy and see truncated
  output with no error, that's the signal to check the proxy's SSE/timeout
  configuration rather than the SDK. See
  [Architecture: the truncation rule](architecture.md).

## Tool-calling issues

- **`*ai.NoSuchToolError`** — the model requested a tool name that isn't in
  `Tools`, or isn't in the active set. If you're using `ActiveTools` to
  restrict which tools are offered, remember a tool listed in `Tools` but
  excluded from `ActiveTools` is treated as unknown if the model somehow
  still calls it — a `nil` `ActiveTools` (the default) means every tool in
  `Tools` is active.
- **A failing tool call keeps failing the same way** — `RepairToolCall`,
  when set, gets exactly one chance to fix a failing call; whatever it
  returns is re-validated once, but `RepairToolCall` is *not* invoked a
  second time for that original call. If the repaired call also fails,
  normal failure semantics apply (`*NoSuchToolError` aborts the batch;
  other errors are recorded and the loop continues).
- See [Tools](core/tools.md#activetools) for the full error taxonomy and
  the `RepairToolCall` single-shot rule.

## Which provider supports X?

Don't guess — check the capability matrix in
[Provider overview](providers/README.md#capability-matrix) (chat, tools,
structured output, embeddings, images, speech, transcription) before
assuming a feature is missing or broken. The matrix is also summarized in
the top-level [README](../README.md#provider-and-capability-matrix) and,
for media specifically, in [Media](core/media.md).

## MCP connection debugging

- **Server process starts but nothing seems to happen** — `NewStdioTransport`
  passes the child process's stderr straight through to the calling
  process, so server-side startup errors and logs show up on your own
  stderr; check there first before assuming the client is hanging.
- **A protocol/version-mismatch error on `Initialize`** — this client
  speaks a single pinned protocol version; a server that only speaks a
  different version will reject the handshake. There's no negotiation
  fallback in v1 — see [MCP](mcp.md#transports-documented-deviations) for
  the exact version and the other documented transport deviations.
- **A tool call returns an error you didn't expect** — server-side
  JSON-RPC errors come back as `*mcp.RPCError` (`errors.As`-able, with
  `Code`/`Message`); anything else (closed transport, context timeout,
  malformed wire JSON) is returned unwrapped. See
  [MCP: error handling](mcp.md#error-handling).
- Remember the v1 client is tools-only (no resources, prompts, sampling,
  or roots) and text-content-only for tool results — see
  [MCP: limitations](mcp.md#limitations-v1) before assuming a missing
  feature is a bug.

## Source of truth

This page collects pointers into the deeper guides; nothing here is
authoritative on its own. Follow the links to
[Errors and retries](core/errors-and-retries.md),
[Structured output](core/structured-output.md),
[Streaming](core/streaming.md), [Tools](core/tools.md),
[Provider overview](providers/README.md), and [MCP](mcp.md) for the full
detail.
