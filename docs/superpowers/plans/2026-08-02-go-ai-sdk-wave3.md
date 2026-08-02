# go-ai-sdk Wave 3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the cloud-platform providers — Azure OpenAI, Google Vertex AI, Amazon Bedrock — with stdlib-only auth (api-key header / OAuth2 service-account JWT / SigV4), plus clear the accumulated hardening backlog from waves 1–2.

**Architecture:** Azure rides `internal/openaicompat` via a new `APIKeyHeader` config knob. Vertex rides a new `internal/geminicompat` extracted from `providers/google` (URL-builder + authorizer injection), with OAuth2 service-account token minting in `internal/gauth`. Bedrock is standalone: SigV4 signing in `internal/sigv4`, AWS binary event-stream parsing in `internal/eventstream`, and the Bedrock Converse/ConverseStream APIs.

**Tech Stack:** Go 1.26, stdlib only (crypto/rsa+sha256 for JWT, crypto/hmac for SigV4, hash/crc32 for eventstream).

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- Providers NEVER retry, loop tools, or parse objects.
- Existing tests stay green after every task; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit.
- StreamResponse disciplines (wave-1 conventions, mirror exactly): single-use `Parts()`, every yield checked, idempotent `Close()`, ctx cancellation passthrough, exactly one `FinishPart`, truncation rule (finish-reason seen → FinishPart at EOF; never seen → `Err()`), `IsError`-slot comments.
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Hardening backlog (deferred minors from waves 1–2 — all of them)

**Files:**
- Modify: `providers/cohere/wire.go` (+ its tests), `internal/openaicompat/embedding.go`, `providers/mistral/embedding.go` (+ tests), `internal/openaicompat/compattest/compattest.go`, `provider/providertest/providertest.go`, `provider/stream.go`, `provider/message.go`, `ai/stream_text.go`, `ai/stream_object.go`

**Work items (each gets a covering test where behavior changes):**

1. **Cohere native tool_choice + unmatched-name error** (`providers/cohere/wire.go`): Cohere v2 DOES have a `tool_choice` field accepting `"REQUIRED"` and `"NONE"`. Change the mapping: `ToolChoiceRequired` → `tool_choice:"REQUIRED"` with all tools; `ToolChoiceNone` → keep omitting tools (unchanged); `ToolChoiceTool` → single tool def + `tool_choice:"REQUIRED"`; auto → field omitted. Fix the now-wrong comment ("has no tool_choice field at all"). NEW: `ToolChoiceTool` whose `ToolName` matches none of `call.Tools` → return `error` from the request builder (`fmt.Errorf("cohere: tool choice %q not in provided tools", name)`) instead of silently sending zero tools. Tests: required→REQUIRED serialized; unmatched name → error.
2. **Embedding completeness checks** (`internal/openaicompat/embedding.go`, `providers/mistral/embedding.go`): after re-indexing by `data[i].index`, verify every slot `[0,len(values))` was populated; any nil slot → `fmt.Errorf("<name>: embedding response missing index %d", i)`. Tests: response omitting one index → error (both packages; mistral test file exists, openaicompat needs a small `embedding_test.go`).
3. **compattest goroutine-safe failures** (`compattest.go`): replace every `t.Fatalf` inside HTTP handler funcs with `t.Errorf` + `http.Error(w, msg, 500)` + `return` (t.Errorf is goroutine-safe; Fatalf is not per testing docs). No behavior change for passing tests.
4. **providertest Stream-cancel scenario** (`provider/providertest/providertest.go`): add subtest `Cancel/Stream`: context canceled before `Stream(...)` → `errors.Is(err, context.Canceled)`. This runs automatically for all 12 existing providers via their conformance wiring (no fixture changes needed — the HTTP client aborts pre-dispatch). Run `go test ./providers/...` to prove all 12 pass it; any provider that fails gets fixed in this task.
5. **Docs-in-code**: `provider/stream.go` — doc note on `ToolCallDelta.Name`: "may be repeated on every fragment for a given ID; consumers must treat repeats as idempotent" (matches all-provider behavior). `provider/message.go` — doc note on `FilePart`: "no built-in provider currently supports FilePart; providers return a descriptive error". Verify each provider's message converter actually returns a descriptive error for `FilePart` (grep; add the error + a one-line test where any provider silently drops it instead). `ai/stream_text.go` + `ai/stream_object.go` — doc note on `Close()`: "not safe for concurrent use with Parts()".

- [ ] **Step 1: Work items 1–2 with failing tests first → implement → pass.**
- [ ] **Step 2: Work items 3–5; full `go test ./...` proves item 4 across all 12 providers.**
- [ ] **Step 3: Full check suite. Commit** — `fix: harden cohere tool_choice, embedding completeness, test-suite discipline`

---

### Task 2: Azure OpenAI provider

**Files:**
- Modify: `internal/openaicompat/openaicompat.go` (Config + auth header logic), `internal/openaicompat/compattest/compattest.go` (header capture accessor)
- Create: `providers/azure/azure.go`
- Test: `providers/azure/azure_test.go`, extend `internal/openaicompat/wire_requestshape_test.go`

**Interfaces:**
- Produces: `openaicompat.Config` gains `APIKeyHeader string` — empty (default) → `Authorization: Bearer <key>`; non-empty → header `<APIKeyHeader>: <key>` (no Bearer prefix). `compattest.Server` gains `func (s *Server) HeaderValues(name string) []string` (the named header of each request, in arrival order).
- Produces: `providers/azure` package:

```go
// Azure OpenAI via the v1 (OpenAI-compatible) surface. Model IDs are Azure
// DEPLOYMENT names.
func New(opts ...Option) *Provider
// Options: WithAPIKey (env AZURE_API_KEY), WithResourceName (env
// AZURE_RESOURCE_NAME; sets base https://{resource}.openai.azure.com/openai/v1),
// WithBaseURL (full override, wins over resource name), WithHTTPClient.
func (p *Provider) Model(id string) provider.LanguageModel        // Name "azure", NativeJSON true, APIKeyHeader "api-key"
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel // EmbedBatch 2048
```

`New` with neither resource name nor base URL: defer the error to request time is messy — instead `Model`/`EmbeddingModel` construct with `BaseURL: ""` and the language model's first call returns `*ai.APICallError`-style error; simpler and testable: `New` stores `baseURL == ""` and `Model` panics is wrong — DECISION: requests against an empty base URL return `fmt.Errorf("azure: no resource name or base URL configured")` from Generate/Stream/Embed (openaicompat: add an early check `if cfg.BaseURL == ""` in its request builders — generic message `"<name>: base URL not configured"`). Test it.

- [ ] **Step 1: Failing tests** — openaicompat request-shape test: Config with `APIKeyHeader:"api-key"` sends `api-key: k` and no `Authorization` header; azure conformance test via compattest (`providertest.Run`, ProviderName "azure") + `TestDefaults` (resource name → base URL) + `TestAPIKeyHeaderSent` (via `HeaderValues("api-key")`) + `TestNoBaseURLErrors`.
- [ ] **Step 2: Implement Config knob, compattest accessor, azure package → GREEN.**
- [ ] **Step 3: Full check suite. Commit** — `feat: Azure OpenAI provider`

---

### Task 3: Extract `internal/geminicompat` from `providers/google`

**Files:**
- Create: `internal/geminicompat/{geminicompat.go,language_model.go,wire.go,embedding.go}` (moved from `providers/google/`, parameterized)
- Modify: `providers/google/google.go` (delegate), delete moved files
- Test: existing `providers/google` tests stay green with zero substantive edits (same guard as the wave-2 openaicompat extraction; white-box wire tests may relocate into geminicompat if they touch now-unexported symbols — assertions intact)

**Interfaces:**
- Produces:

```go
package geminicompat

type Config struct {
    Name       string
    HTTPClient *http.Client
    // EndpointFor returns the full URL for a model call. method is one of
    // "generateContent", "streamGenerateContent" (caller appends ?alt=sse),
    // "batchEmbedContents".
    EndpointFor func(modelID, method string) string
    // Authorize mutates the request with auth (header). Called per request.
    Authorize func(ctx context.Context, req *http.Request) error
    EmbedBatch int
}
func NewLanguageModel(cfg Config, modelID string) provider.LanguageModel // Capabilities{NativeJSON: true}
func NewEmbeddingModel(cfg Config, modelID string) provider.EmbeddingModel
```

`providers/google` keeps its exact public API: `EndpointFor` builds `{base}/models/{id}:{method}`, `Authorize` sets `x-goog-api-key`. All existing google behavior identical (the extraction guard proves it).

- [ ] **Step 1: Move + parameterize; `go test ./providers/google/ -v` green with only mechanical adjustments.**
- [ ] **Step 2: Full check suite. Commit** — `refactor: extract internal/geminicompat from google provider`

---

### Task 4: Google Vertex AI provider (+ `internal/gauth`)

**Files:**
- Create: `internal/gauth/gauth.go`, `providers/vertex/vertex.go`, `providers/vertex/embedding.go`
- Test: `internal/gauth/gauth_test.go`, `providers/vertex/vertex_test.go`, `providers/vertex/embedding_test.go`

**Interfaces:**
- Produces `internal/gauth`:

```go
// TokenSource yields a bearer token for Google Cloud APIs.
type TokenSource interface{ Token(ctx context.Context) (string, error) }

type StaticTokenSource string // Token returns string(s), never errors

// ServiceAccountTokenSource mints OAuth2 access tokens from a service-account
// key (JSON with client_email + private_key PEM). Flow: build RS256 JWT
// {iss:email, scope:"https://www.googleapis.com/auth/cloud-platform",
// aud:tokenURL, iat, exp:+1h}, POST tokenURL with
// grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=<jwt>,
// parse {access_token, expires_in}. Caches until expiry-60s; mutex-guarded.
// tokenURL defaults to https://oauth2.googleapis.com/token, overridable for
// tests.
func NewServiceAccountTokenSource(keyJSON []byte) (*ServiceAccountTokenSource, error)
func NewServiceAccountTokenSourceFromFile(path string) (*ServiceAccountTokenSource, error)
func (s *ServiceAccountTokenSource) SetTokenURL(u string) // test hook
func (s *ServiceAccountTokenSource) Token(ctx context.Context) (string, error)
```

JWT: header `{"alg":"RS256","typ":"JWT"}`; segments base64.RawURLEncoding; signature `rsa.SignPKCS1v15(nil, key, crypto.SHA256, sha256(input))`; PEM parse via `encoding/pem` + `x509.ParsePKCS8PrivateKey` (fall back to `ParsePKCS1PrivateKey`). gauth tests: httptest token endpoint verifying the JWT decodes + signature verifies against the test key's public half, caching (second call no HTTP), expiry refresh.

- Produces `providers/vertex`:

```go
// Vertex AI (Gemini models on Google Cloud).
func New(opts ...Option) *Provider
// Options: WithProject (env GOOGLE_VERTEX_PROJECT), WithLocation (env
// GOOGLE_VERTEX_LOCATION, default "us-central1"), WithTokenSource(gauth.TokenSource),
// WithAccessToken(string) (wraps StaticTokenSource), WithHTTPClient,
// WithBaseURL (override; default https://{location}-aiplatform.googleapis.com/v1).
// When no token source is given, New checks GOOGLE_APPLICATION_CREDENTIALS and
// uses NewServiceAccountTokenSourceFromFile; if that env is unset too, requests
// error with "vertex: no credentials configured".
func (p *Provider) Model(id string) provider.LanguageModel // Name "vertex"
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel // MaxBatchSize 250
```

Language model = `geminicompat.NewLanguageModel` with `EndpointFor` → `{base}/projects/{project}/locations/{location}/publishers/google/models/{id}:{method}` and `Authorize` → `Authorization: Bearer <ts.Token(ctx)>`. Embeddings do NOT use geminicompat (Vertex uses `:predict`): `POST …models/{id}:predict` `{"instances":[{"content":<text>}...]}` → `predictions[i].embeddings.values` (`[]float64`), `statistics.token_count` summed into Usage. Own `embedding.go`.

- [ ] **Step 1: gauth failing tests → implement → pass.**
- [ ] **Step 2: vertex conformance (fixture = Gemini wire format at vertex paths, static token source; assert Authorization header + URL path shape) + embedding predict test (3 values) + no-credentials error test → implement → pass.**
- [ ] **Step 3: Full check suite. Commit** — `feat: Vertex AI provider with service-account auth`

---

### Task 5: `internal/sigv4` + `internal/eventstream` + Bedrock language model

**Files:**
- Create: `internal/sigv4/sigv4.go`, `internal/eventstream/eventstream.go`, `providers/bedrock/{bedrock.go,language_model.go,wire.go}`
- Test: `internal/sigv4/sigv4_test.go`, `internal/eventstream/eventstream_test.go`, `providers/bedrock/bedrock_test.go`

**Interfaces:**
- Produces `internal/sigv4`:

```go
type Credentials struct{ AccessKeyID, SecretAccessKey, SessionToken string }
// Sign adds X-Amz-Date, X-Amz-Content-Sha256, optional X-Amz-Security-Token,
// and Authorization (AWS4-HMAC-SHA256) to req for the given body/region/service.
// now is injected for testability.
func Sign(req *http.Request, body []byte, creds Credentials, region, service string, now time.Time) error
```

Canonical request per AWS SigV4 spec: method, canonical URI (path segments URI-encoded), canonical query (sorted, encoded), canonical headers (lowercase, sorted; sign host + all x-amz-*+content-type present), signed headers list, hex(sha256(body)). String to sign: `AWS4-HMAC-SHA256\n<ISO8601>\n<date>/<region>/<service>/aws4_request\nhex(sha256(canonicalRequest))`. Key chain: HMAC("AWS4"+secret, date)→region→service→"aws4_request". Test with the AWS documentation's published test vector (get-vanilla style: fixed date 20130524, region us-east-1, service s3 example — assert exact Authorization string) plus a session-token case.

- Produces `internal/eventstream`:

```go
type Message struct {
    Headers map[string]string // string-typed headers (type 7); others skipped
    Payload []byte
}
// Scan parses application/vnd.amazon.eventstream frames:
// [4B total len][4B headers len][4B prelude CRC32][headers][payload][4B message CRC32]
// Header: [1B name len][name][1B value type][2B value len][value] (type 7 = string).
// CRC32 = IEEE. Invalid CRC or truncated frame → yields the error and stops.
func Scan(r io.Reader) iter.Seq2[Message, error]
// Encode builds one frame (used by tests and fixtures).
func Encode(headers map[string]string, payload []byte) []byte
```

Round-trip tests + corrupted-CRC test + truncated-frame test.

- Produces `providers/bedrock`:

```go
// Amazon Bedrock via the Converse API.
func New(opts ...Option) *Provider
// Options: WithRegion (env AWS_REGION, default "us-east-1"),
// WithCredentials(accessKeyID, secretAccessKey, sessionToken) (env
// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN),
// WithBaseURL (default https://bedrock-runtime.{region}.amazonaws.com),
// WithHTTPClient.
func (p *Provider) Model(id string) provider.LanguageModel // Name "bedrock", NativeJSON false (tool-mode objects)
```

Wire (Converse): `POST {base}/model/{url-escaped id}/converse`; body `{system:[{text}], messages:[{role, content:[{text}|{image:{format,source:{bytes:b64}}}|{toolUse:{toolUseId,name,input:<obj>}}|{toolResult:{toolUseId,content:[{json:<obj>}|{text}],status:"error"(when IsError)}}]}], toolConfig:{tools:[{toolSpec:{name,description,inputSchema:{json:<schema obj>}}}], toolChoice:{auto:{}}|{any:{}}|{tool:{name}}}, inferenceConfig:{maxTokens,temperature,topP,stopSequences}}`. ToolChoiceNone → omit toolConfig entirely. Response: `{output:{message:{content:[...]}}, stopReason, usage:{inputTokens,outputTokens,totalTokens}}`; stopReason end_turn→stop, max_tokens→length, tool_use→tool-calls, stop_sequence→stop, content_filtered→content-filter, else other. Bedrock has no response-format → `ResponseFormat` ignored with a comment (NativeJSON false so core uses tool-mode anyway).
Streaming: `POST …/converse-stream`, response is eventstream; each Message has header `:event-type` ∈ messageStart | contentBlockStart (`{start:{toolUse:{toolUseId,name}}}`) | contentBlockDelta (`{delta:{text}}` → TextDelta; `{delta:{toolUse:{input:<partial json string>}}}` → ToolCallDelta) | contentBlockStop (tool block → ToolCallEnd, empty args → `"{}"`) | messageStop (`{stopReason}`) | metadata (`{usage}`); `:message-type` `exception` → Err(). Emit single FinishPart at stream end from captured stopReason+usage; stopReason never seen → Err(). Errors: non-2xx → `ai.NewAPICallError` (parse `{"message":...}`).
Fixture: httptest handler using `eventstream.Encode` for stream scenarios; assert `Authorization` contains `AWS4-HMAC-SHA256` and `SignedHeaders=`, and `X-Amz-Date` present. providertest.Run (ProviderName "bedrock") + request-shape tests (toolSpec/inputSchema/toolChoice/inferenceConfig, toolResult status:"error") + both stream-robustness cases.

- [ ] **Step 1: sigv4 tests (AWS vector) → implement → pass. Step 2: eventstream tests → implement → pass.**
- [ ] **Step 3: bedrock conformance RED → implement → GREEN; request-shape + robustness tests.**
- [ ] **Step 4: Full check suite. Commit** — `feat: Amazon Bedrock provider (Converse API, SigV4, eventstream)`

---

### Task 6: Bedrock embeddings (Titan) + docs

**Files:**
- Create: `providers/bedrock/embedding.go`
- Test: `providers/bedrock/embedding_test.go`
- Modify: `README.md`, `docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md`

**Interfaces:**
- Produces: `func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel` — Titan embeddings via `POST {base}/model/{id}/invoke` (SigV4-signed), body `{"inputText": <one value>}` → `{"embedding":[...], "inputTextTokenCount":n}`. Titan accepts ONE text per call → `MaxBatchSize() == 1` (ai.EmbedMany loops); Embed(values) with len>1 → error. Usage: inputTextTokenCount summed.
- Docs: README capability table + roadmap gain azure/vertex/bedrock (embeddings: azure yes, vertex yes, bedrock yes; structured output: azure native, vertex native, bedrock tool-mode footnote); spec waves table wave-3 row "(shipped)".

- [ ] **Step 1: Embedding test (fixture asserts inputText body + SigV4 header) → implement → pass.**
- [ ] **Step 2: Docs updated, every cell verified against code (header/delimiter cell counts checked on all tables).**
- [ ] **Step 3: Full check suite. Commit** — `feat: Bedrock Titan embeddings; docs: wave 3 roster`

---

## Self-Review Notes

- **Coverage:** spec wave-3 row = Azure (T2), Vertex (T3–T4), Bedrock (T5–T6); hardening backlog cleared in T1 per the no-deferral directive.
- **Type consistency:** `openaicompat.Config.APIKeyHeader` (T2) and `compattest.HeaderValues` (T2) consumed only in T2; `geminicompat.Config{Name, HTTPClient, EndpointFor, Authorize, EmbedBatch}` produced T3, consumed T4; `gauth.TokenSource`/`StaticTokenSource`/`ServiceAccountTokenSource` produced and consumed in T4; `sigv4.Sign` + `eventstream.Scan/Encode` produced T5, `Encode` reused by T5/T6 fixtures.
- **providertest change (T1 item 4)** is an intentional contract extension (new Cancel/Stream subtest), justified by the no-deferral directive; T1 proves all 12 existing providers pass it before any new provider is built against it.
- **No placeholders**; exact URLs, env vars, wire shapes, and auth flows are pinned above.
