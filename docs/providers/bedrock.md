# Amazon Bedrock

`providers/bedrock` talks to Amazon Bedrock's Converse and ConverseStream
APIs, signing every request with AWS Signature Version 4 — there is no API
key; authentication is AWS credentials.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/bedrock"

p := bedrock.New(
	bedrock.WithRegion("us-east-1"), // defaults to AWS_REGION, else AWS_DEFAULT_REGION, else "us-east-1"
	bedrock.WithCredentials(accessKeyID, secretAccessKey, sessionToken), // defaults to AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN
	bedrock.WithBaseURL("https://bedrock-runtime.us-east-1.amazonaws.com"), // the default, derived from region
	bedrock.WithHTTPClient(http.DefaultClient),
)

model := p.Model("anthropic.claude-3-sonnet-20240229-v1:0")
```

## Credentials and signing

`New()` reads `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`/`AWS_SESSION_TOKEN`
into `sigv4.Credentials` by default; `WithCredentials` overrides all three
explicitly. **This is not the full AWS SDK default credential chain** — no
`~/.aws/credentials` profile file, no EC2/ECS/Lambda instance-role metadata
endpoint, no SSO. If your deployment relies on one of those, resolve the
credentials yourself (e.g. with the AWS SDK for Go) and pass the resulting
access key/secret/session token to `WithCredentials`.
(`providers/bedrock/bedrock.go:37-47, 60-86`.)

Every request — Converse, ConverseStream, and the Titan embeddings
`/invoke` call — goes through `sigv4.Sign(httpReq, body, creds, region,
"bedrock", time.Now())` before being sent
(`providers/bedrock/language_model.go:74-90`, `Provider.doRequest`). Region
is resolved once at `New()` time and baked into both the signing scope and
the default base URL (`https://bedrock-runtime.{region}.amazonaws.com`);
there's no per-request region override.

Model IDs (which routinely contain `:`, e.g.
`anthropic.claude-3-sonnet-20240229-v1:0`) are percent-encoded specifically
for SigV4 correctness: `escapeModelID` encodes everything outside SigV4's
unreserved set (`A-Z a-z 0-9 - _ . ~`), because `url.PathEscape` alone
leaves `:` unescaped (technically legal in a path segment) while Bedrock
expects it percent-encoded and the SigV4 canonical request must match the
exact bytes sent on the wire. (`providers/bedrock/language_model.go:45-65`.)

## Supported capabilities

- **Text generation & streaming** — `p.Model(id)`, via the Converse /
  ConverseStream APIs. Streaming decodes the AWS event-stream binary
  framing (`internal/eventstream`), not SSE.
- **Tool calling** — same `Model(id)`, via Converse's `toolConfig`.
- **Structured output** — no native JSON mode:
  `Capabilities().NativeJSON` is `false`
  (`providers/bedrock/language_model.go:28-32`, doc comment: *"Bedrock's
  Converse API has no schema-constrained JSON response mode; the ai core
  falls back to tool-mode object generation"*) — same tool-mode fallback as
  Anthropic, via `ai.GenerateObject`'s forced-tool-choice path.
- **Reasoning (`reasoningContent`)** — Converse's signed/redacted reasoning
  blocks map to `provider.ReasoningPart` the same way Anthropic's thinking
  blocks do; see [Reasoning](../core/reasoning.md#bedrock-reasoningcontent).
- **Document attachments** — a fixed set of Converse document format codes
  (below), not arbitrary media types.
- **Embeddings (Amazon Titan)** — `p.EmbeddingModel(id)`, e.g.
  `p.EmbeddingModel("amazon.titan-embed-text-v2:0")`, against Titan's
  `/invoke` endpoint.
- **No image generation, no speech, no transcription** — not exposed by
  this package.

## Quirks and notes

- **Titan embeddings accept exactly one input per call.** `MaxBatchSize()`
  returns `1` (`embeddingMaxBatchSize`,
  `providers/bedrock/embedding.go:12-16`) — Titan's `/invoke` wire contract
  is one `inputText` per request with no batched shape, unlike Bedrock's
  other embedding models ("which vary", per the source comment). `ai.EmbedMany`
  respects `MaxBatchSize()` and issues one call per value automatically; a
  direct call to `Embed` with more than one value returns an error instead
  of silently truncating or reordering. (`providers/bedrock/embedding.go:46-54`.)
- **Document format codes are a small, closed, erroring set** (unlike image
  media types, which fall back to `png` for anything unrecognized). A
  `FilePart` with a `MediaType` outside this list returns an error:

  | `MediaType` | Converse format code |
  |---|---|
  | `application/pdf` | `pdf` |
  | `text/csv` | `csv` |
  | `text/html` | `html` |
  | `text/plain` | `txt` |
  | `text/markdown` | `md` |
  | `application/msword` | `doc` |
  | `application/vnd.openxmlformats-officedocument.wordprocessingml.document` | `docx` |
  | `application/vnd.ms-excel` | `xls` |
  | `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` | `xlsx` |

  (`providers/bedrock/wire.go:482-499`, `documentFormat`.)
- **Image and document parts require inline `Data`** — a `URL`-only
  `ImagePart` (no bytes) is rejected outright: *"image parts require inline
  Data (URL images are not supported)"*. Bedrock's Converse API has no
  fetch-by-URL image ingestion. (`providers/bedrock/wire.go:438-441`.)
- **Reasoning blocks lead the assistant turn on replay**, mirroring
  Anthropic: `assistantBlocks` partitions `reasoningContent` blocks out and
  prepends them ahead of text/tool-use blocks, since Converse likewise
  requires reasoning content to come first. A non-redacted reasoning part
  with no signature is skipped (unreplayable) for the same reason as
  Anthropic's thinking blocks. (`providers/bedrock/wire.go:385-430`.)
- **Tool results have a dedicated error slot.** Unlike Mistral/OpenAI-style
  tool messages (plain text/JSON with no error flag), Bedrock's
  `toolResult` content block has a `status: "error"` field, set when
  `ToolResultPart.IsError` is true. (`providers/bedrock/wire.go:368-374`.)
- **`ToolChoiceNone` omits `toolConfig` entirely**; a tool-choice with zero
  tools is rejected fast, client-side, with a descriptive error rather than
  being sent to the API to bounce: *"bedrock: tool choice requires at least
  one tool"*. (`providers/bedrock/wire.go:276-298`.)
- **Streaming distinguishes transport errors from modeled exceptions.** An
  event-stream message with `:message-type: exception` is an
  application-level error (JSON payload with a `message` field); one with
  `:message-type: error` is a transport-level failure carried in
  `:error-code`/`:error-message` headers, not a payload — both terminate the
  stream, but are reported with different error text.
  (`providers/bedrock/language_model.go:205-237`.)

## ProviderOptions

Entries under `ProviderOptions["bedrock"]` are shallow-merged into the raw
Converse request body verbatim. Verified directly against
`providers/bedrock/provideroptions_test.go`: an option key can override an
SDK-built nested field (`inferenceConfig.maxTokens`), and a novel top-level
key not otherwise exposed by the SDK —
**`additionalModelRequestFields`**, Converse's escape hatch for
model-specific parameters like Anthropic's `top_k` when running a Claude
model through Bedrock — passes straight through wholesale:

```go
result, err := ai.GenerateText(context.Background(), ai.GenerateTextOpts{
	Model:  p.Model("anthropic.claude-3-sonnet-20240229-v1:0"),
	Prompt: "Explain the Go scheduler in one sentence.",
	ProviderOptions: map[string]any{
		"bedrock": map[string]any{
			"inferenceConfig":              map[string]any{"maxTokens": 99}, // overrides Call.MaxTokens
			"additionalModelRequestFields": map[string]any{"top_k": 5},      // passthrough, set wholesale
		},
	},
})
```

## Source of truth

- [`providers/bedrock/bedrock.go`](../../providers/bedrock/bedrock.go)
  (package doc comment, `Option`s, region/credential resolution)
- [`providers/bedrock/language_model.go`](../../providers/bedrock/language_model.go)
  (`Capabilities`, `doRequest`/`sigv4.Sign`, `escapeModelID`, streaming
  exception vs. transport-error handling)
- [`providers/bedrock/wire.go`](../../providers/bedrock/wire.go)
  (`buildConverseRequest`, `assistantBlocks`, `documentFormat`,
  `applyProviderOptions`)
- [`providers/bedrock/embedding.go`](../../providers/bedrock/embedding.go)
  (Titan `/invoke`, `embeddingMaxBatchSize`)
- [`providers/bedrock/provideroptions_test.go`](../../providers/bedrock/provideroptions_test.go)
- [`internal/sigv4`](../../internal/sigv4) (SigV4 signing)

See also: [Reasoning](../core/reasoning.md) for the `reasoningContent`
worked example shared with Anthropic; [Media](../core/media.md) for the
full `FilePart` attachment matrix across all providers.
