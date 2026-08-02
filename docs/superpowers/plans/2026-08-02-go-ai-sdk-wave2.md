# go-ai-sdk Wave 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship wave 2 of the provider roster: seven OpenAI-compatible presets (Groq, xAI, DeepSeek, Cerebras, Together, Fireworks, Perplexity) as thin wrappers over an extracted shared base, plus full Mistral and Cohere providers.

**Architecture:** Extract the existing `providers/openai` wire logic into `internal/openaicompat` (config-driven), make `providers/openai` the first preset of it, then add each wave-2 preset as a ~50-line package. Mistral and Cohere get standalone implementations following the structural template established by `providers/anthropic` and `providers/google`. Every provider passes the existing `provider/providertest` conformance suite — that suite is the unchanged contract.

**Tech Stack:** Go 1.26, stdlib only. Existing packages: `provider` (spec), `ai` (core, incl. `NewAPICallError`), `internal/sse`, `provider/providertest`, reference providers `providers/{openai,anthropic,google}`.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- Providers NEVER retry, loop tools, or parse objects — translation only.
- `provider/providertest` is the contract and MUST NOT be modified by any task in this plan.
- All existing tests must remain green after every task (`go test ./...`).
- `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit (CI enforces gofmt).
- StreamResponse discipline (established in wave 1, mirror it): single-use `Parts()`, every yield's return value checked, idempotent `Close()`, ctx cancellation passthrough, exactly one `FinishPart` per stream, truncation rule = finish-reason-seen → emit FinishPart at EOF; never-seen → set `Err()`.
- `ToolResultPart.IsError` handling per provider documented with a comment when the wire format has no slot (convention from `providers/openai/wire.go`).
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: Extract `internal/openaicompat` + `compattest` fixture; `providers/openai` becomes preset #1

**Files:**
- Create: `internal/openaicompat/openaicompat.go` (Config + constructors), `internal/openaicompat/language_model.go`, `internal/openaicompat/wire.go`, `internal/openaicompat/embedding.go` (moved from `providers/openai/`, renamed package, config-driven)
- Create: `internal/openaicompat/compattest/compattest.go` (shared fixture server)
- Modify: `providers/openai/openai.go` (delegate to openaicompat), delete `providers/openai/{language_model,wire,embedding}.go`
- Test: existing `providers/openai/*_test.go` stay green (adapt imports/visibility only where the move forces it — the integration test and conformance tests are black-box `package openai_test`/`package openai` and must keep passing unmodified in substance)

**Interfaces:**
- Consumes: current `providers/openai` implementation (the code being moved), `provider` types, `ai.NewAPICallError`, `internal/sse`.
- Produces (used verbatim by Tasks 2–3):

```go
// internal/openaicompat/openaicompat.go
package openaicompat

type Config struct {
    Name       string        // provider.LanguageModel.ProviderName() value, e.g. "groq"
    APIKey     string
    BaseURL    string        // no trailing slash, e.g. "https://api.groq.com/openai/v1"
    HTTPClient *http.Client  // nil → http.DefaultClient
    NativeJSON bool          // Capabilities().NativeJSON
    EmbedBatch int           // EmbeddingModel.MaxBatchSize(); callers only construct embedding models when > 0
}
func NewLanguageModel(cfg Config, modelID string) provider.LanguageModel
func NewEmbeddingModel(cfg Config, modelID string) provider.EmbeddingModel

// internal/openaicompat/compattest/compattest.go
package compattest

// NewFixtureServer returns an httptest.Server speaking the OpenAI
// chat-completions + embeddings wire format, implementing the six
// providertest scenarios keyed off the LAST user message text, with
// "simple" responding "Hello from <providerName>!". Callers must Close it.
// It also records raw request bodies: Requests() returns them in order.
func NewFixtureServer(t *testing.T, providerName string) *Server

type Server struct { *httptest.Server }
func (s *Server) Requests() [][]byte // JSON bodies of chat/embeddings requests, in arrival order
```

The moved code is a rename+parameterization of the existing files: `providerName()` returns `cfg.Name` instead of the literal `"openai"`; base URL / key / client come from `cfg`; `Capabilities()` returns `provider.Capabilities{NativeJSON: cfg.NativeJSON}`; embedding `MaxBatchSize()` returns `cfg.EmbedBatch`. No behavior changes.

`providers/openai/openai.go` keeps its exact public API and defaults:

```go
package openai

type Option func(*Provider)
func WithAPIKey(k string) Option
func WithBaseURL(u string) Option
func WithHTTPClient(c *http.Client) Option
func New(opts ...Option) *Provider   // key default os.Getenv("OPENAI_API_KEY"), base "https://api.openai.com/v1"
func (p *Provider) Model(id string) provider.LanguageModel        // cfg.Name "openai", NativeJSON true
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel // EmbedBatch 2048
```

The compattest fixture is extracted from the fixture server currently inside `providers/openai/openai_test.go` (six scenarios + SSE). The openai package's request-shape tests, stream-robustness tests, and `integration_test.go` remain in `providers/openai` and keep passing; the conformance test may switch to the shared fixture.

- [ ] **Step 1: Create `internal/openaicompat` by moving the three implementation files**, parameterizing on `Config` per the Produces block. Run `go build ./...` until clean.
- [ ] **Step 2: Rewrite `providers/openai` as the preset** (openai.go only, delegating; delete moved files). `go build ./...` clean.
- [ ] **Step 3: Run the full openai test suite** — `go test ./providers/openai/ -v`. Expected: PASS with zero substantive test edits (import/package adjustments only). If a test needed weakening to pass, that is a defect — stop and fix the extraction instead.
- [ ] **Step 4: Extract the fixture server into `internal/openaicompat/compattest`** with the `NewFixtureServer`/`Requests` API above; wire the openai conformance test to use it (proves the fixture works before Tasks 2–3 depend on it).
- [ ] **Step 5: Full check suite (`go vet ./... && go test ./... && gofmt -l .`) → all green. Commit** — `refactor: extract internal/openaicompat base from openai provider`

---

### Task 2: Presets — groq, xai, deepseek, cerebras (no embeddings)

**Files:**
- Create: `providers/groq/groq.go`, `providers/xai/xai.go`, `providers/deepseek/deepseek.go`, `providers/cerebras/cerebras.go`
- Test: `providers/groq/groq_test.go`, `providers/xai/xai_test.go`, `providers/deepseek/deepseek_test.go`, `providers/cerebras/cerebras_test.go`

**Interfaces:**
- Consumes: `openaicompat.Config`/`NewLanguageModel` (Task 1), `compattest.NewFixtureServer` (Task 1), `providertest.Run`.
- Produces: four packages each exporting exactly `Option`, `WithAPIKey`, `WithBaseURL`, `WithHTTPClient`, `New`, `(*Provider).Model` — NO `EmbeddingModel` (none of these four offer embeddings).

Preset values (exact):

| Package | Name | API key env | Default BaseURL | NativeJSON |
|---|---|---|---|---|
| groq | groq | `GROQ_API_KEY` | `https://api.groq.com/openai/v1` | true |
| xai | xai | `XAI_API_KEY` | `https://api.x.ai/v1` | true |
| deepseek | deepseek | `DEEPSEEK_API_KEY` | `https://api.deepseek.com/v1` | true |
| cerebras | cerebras | `CEREBRAS_API_KEY` | `https://api.cerebras.ai/v1` | true |

Full template (groq shown; the other three differ only in package name, env var, base URL, and doc comment):

```go
// Package groq provides the Groq provider: Groq's API is
// OpenAI-chat-completions compatible, so this package is a preset over the
// shared openaicompat base.
package groq

import (
    "net/http"
    "os"

    "github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
    "github.com/azrtydxb/go-ai-sdk/provider"
)

const defaultBaseURL = "https://api.groq.com/openai/v1"

type Provider struct {
    apiKey     string
    baseURL    string
    httpClient *http.Client
}

type Option func(*Provider)

func WithAPIKey(k string) Option        { return func(p *Provider) { p.apiKey = k } }
func WithBaseURL(u string) Option       { return func(p *Provider) { p.baseURL = u } }
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

func New(opts ...Option) *Provider {
    p := &Provider{apiKey: os.Getenv("GROQ_API_KEY"), baseURL: defaultBaseURL}
    for _, o := range opts {
        o(p)
    }
    return p
}

func (p *Provider) Model(id string) provider.LanguageModel {
    return openaicompat.NewLanguageModel(openaicompat.Config{
        Name:       "groq",
        APIKey:     p.apiKey,
        BaseURL:    p.baseURL,
        HTTPClient: p.httpClient,
        NativeJSON: true,
    }, id)
}
```

Test template (groq shown; same shape for all four):

```go
package groq

import (
    "encoding/json"
    "strings"
    "testing"

    "github.com/azrtydxb/go-ai-sdk/internal/openaicompat/compattest"
    "github.com/azrtydxb/go-ai-sdk/provider"
    "github.com/azrtydxb/go-ai-sdk/provider/providertest"
)

func TestConformance(t *testing.T) {
    srv := compattest.NewFixtureServer(t, "groq")
    defer srv.Close()
    providertest.Run(t, providertest.Config{
        Model:        New(WithAPIKey("test-key"), WithBaseURL(srv.URL)).Model("test-model"),
        ProviderName: "groq",
    })
}

func TestDefaults(t *testing.T) {
    p := New(WithAPIKey("k"))
    if p.baseURL != "https://api.groq.com/openai/v1" {
        t.Fatalf("baseURL = %q", p.baseURL)
    }
    m := p.Model("m")
    if m.ProviderName() != "groq" {
        t.Fatalf("ProviderName = %q", m.ProviderName())
    }
    if !m.Capabilities().NativeJSON {
        t.Fatal("NativeJSON should be true")
    }
}

func TestAuthHeaderAndModelSent(t *testing.T) {
    srv := compattest.NewFixtureServer(t, "groq")
    defer srv.Close()
    m := New(WithAPIKey("secret-k"), WithBaseURL(srv.URL)).Model("llama-test")
    if _, err := m.Generate(t.Context(), provider.Call{Messages: []provider.Message{provider.UserText("simple")}}); err != nil {
        t.Fatal(err)
    }
    reqs := srv.Requests()
    if len(reqs) != 1 {
        t.Fatalf("requests = %d", len(reqs))
    }
    var body struct{ Model string `json:"model"` }
    if err := json.Unmarshal(reqs[0], &body); err != nil || body.Model != "llama-test" {
        t.Fatalf("model in request = %q err=%v", body.Model, err)
    }
    if !strings.Contains(string(reqs[0]), "") { // placeholder-free: real assertion is on the header below
        t.Fatal("unreachable")
    }
}
```

For the auth-header assertion, `compattest.Server` must record headers too — extend `Requests()` design: give `compattest` a second accessor `AuthHeaders() []string` (the `Authorization` header of each request, in order); assert `AuthHeaders()[0] == "Bearer secret-k"`. (Task 1 implements both accessors; this task consumes them — delete the unreachable placeholder block above and assert via `AuthHeaders()`.)

- [ ] **Step 1: Write the four test files** (per template; adjust names/URLs per the preset table). Run them → FAIL (packages don't exist).
- [ ] **Step 2: Implement the four packages** per the template.
- [ ] **Step 3: `go test ./providers/... -v` → all PASS.**
- [ ] **Step 4: Full check suite. Commit** — `feat: groq, xai, deepseek, cerebras providers (openaicompat presets)`

---

### Task 3: Presets — together, fireworks (with embeddings), perplexity

**Files:**
- Create: `providers/together/together.go`, `providers/fireworks/fireworks.go`, `providers/perplexity/perplexity.go`
- Test: `providers/together/together_test.go`, `providers/fireworks/fireworks_test.go`, `providers/perplexity/perplexity_test.go`

**Interfaces:**
- Consumes: same as Task 2.
- Produces: three packages; together and fireworks additionally export `(*Provider).EmbeddingModel(id string) provider.EmbeddingModel`.

Preset values (exact):

| Package | Name | API key env | Default BaseURL | NativeJSON | EmbedBatch |
|---|---|---|---|---|---|
| together | together | `TOGETHER_AI_API_KEY` | `https://api.together.xyz/v1` | true | 100 |
| fireworks | fireworks | `FIREWORKS_API_KEY` | `https://api.fireworks.ai/inference/v1` | true | 100 |
| perplexity | perplexity | `PERPLEXITY_API_KEY` | `https://api.perplexity.ai` | true | — (none) |

Same package template as Task 2. For together/fireworks add:

```go
func (p *Provider) EmbeddingModel(id string) provider.EmbeddingModel {
    return openaicompat.NewEmbeddingModel(openaicompat.Config{
        Name:       "together",
        APIKey:     p.apiKey,
        BaseURL:    p.baseURL,
        HTTPClient: p.httpClient,
        EmbedBatch: 100,
    }, id)
}
```

Perplexity package doc comment must note: `// Perplexity's API does not support tool calling; Tools in a Call are serialized but the live API may reject or ignore them.`

Tests: same three tests as Task 2 per package; together/fireworks additionally run an embedding test against the compattest fixture's `/embeddings` endpoint:

```go
func TestEmbeddings(t *testing.T) {
    srv := compattest.NewFixtureServer(t, "together")
    defer srv.Close()
    em := New(WithAPIKey("k"), WithBaseURL(srv.URL)).EmbeddingModel("embed-model")
    if em.MaxBatchSize() != 100 {
        t.Fatalf("MaxBatchSize = %d", em.MaxBatchSize())
    }
    resp, err := em.Embed(t.Context(), []string{"a", "b", "c"})
    if err != nil {
        t.Fatal(err)
    }
    if len(resp.Embeddings) != 3 {
        t.Fatalf("embeddings = %d", len(resp.Embeddings))
    }
}
```

- [ ] **Step 1: Write the three test files → FAIL. Step 2: Implement. Step 3: `go test ./providers/... -v` → PASS.**
- [ ] **Step 4: Full check suite. Commit** — `feat: together, fireworks, perplexity providers`

---

### Task 4: Mistral provider (full implementation)

**Files:**
- Create: `providers/mistral/mistral.go`, `providers/mistral/language_model.go`, `providers/mistral/wire.go`, `providers/mistral/embedding.go`
- Test: `providers/mistral/mistral_test.go`, `providers/mistral/embedding_test.go`

**Interfaces:**
- Consumes: `provider` types, `ai.NewAPICallError`, `internal/sse`, `providertest.Run`. Structural template: `providers/anthropic` + `providers/openai` (pre-extraction patterns now in `internal/openaicompat` — read both).
- Produces: `mistral.New(opts ...Option) *Provider` (same Option trio), `Model(id) provider.LanguageModel` (Name "mistral", `Capabilities{NativeJSON: true}`), `EmbeddingModel(id) provider.EmbeddingModel` (MaxBatchSize 32). Key env `MISTRAL_API_KEY`; base default `https://api.mistral.ai/v1`.

Wire mapping (Mistral is close to OpenAI's chat completions but NOT identical — implement standalone, do not reuse openaicompat):
- Request: `POST {base}/chat/completions`, `Authorization: Bearer <key>`. Fields: `model`, `messages`, `tools:[{type:"function",function:{name,description,parameters}}]`, `tool_choice`: `"auto"` | `"none"` | `"any"` (Mistral's word for required) | `{"type":"function","function":{"name":...}}`, `response_format:{"type":"json_object"}` (Mistral has no json_schema mode: for `ResponseFormat.Type == "json"` send `json_object` and IGNORE the schema — add a comment noting the schema is enforced by the ai core's decode, not the wire), `max_tokens` (NOT max_completion_tokens), `temperature`, `top_p`, `stop`, `stream`.
- Messages: `system` role supported inline; assistant tool calls `tool_calls:[{id,type:"function",function:{name,arguments:<string>}}]`; tool results as `{role:"tool", tool_call_id, name, content:<string>}` — one message per ToolResultPart (name = ToolResultPart.Name; content = JSON-marshaled result; IsError has no slot → comment, openai convention). Image parts: `{"type":"image_url","image_url":{"url":...}}` content array (data URL for inline bytes).
- Response: `choices[0].message.{content, tool_calls}`; `finish_reason`: `stop`→stop, `length`/`model_length`→length, `tool_calls`→tool-calls, else other; `usage.{prompt_tokens,completion_tokens,total_tokens}`.
- Streaming: SSE `data:` chunks shaped like the response deltas (OpenAI-style: `choices[0].delta.content`, `delta.tool_calls` index-keyed), terminated by `data: [DONE]`; usage arrives on the final content chunk (`usage` field non-null). Same assembly rules as openaicompat: ToolCallDelta per fragment, ToolCallEnd for each assembled call when finish_reason arrives, exactly one FinishPart; truncation rule per Global Constraints.
- Errors: non-2xx → `ai.NewAPICallError(status, url, body, message)` with message from `{"message":...}` or `{"error":{"message":...}}` if parseable.
- Embeddings: `POST {base}/embeddings` `{"model", "input":[...]}` → `data[i].embedding` (re-index by `data[i].index`), `usage.prompt_tokens/total_tokens`. MaxBatchSize 32.

Tests: fixture server speaking the Mistral wire format for the six providertest scenarios (+ Cancel) → `providertest.Run(t, ...{ProviderName: "mistral"})`; request-shape assertions: `tool_choice:"any"` for required, `response_format:{"type":"json_object"}` (and schema absent), `max_tokens` field name; both stream-robustness cases (no [DONE] with finish_reason → one FinishPart; truncated before finish_reason → Err()); embedding test (3 values, out-of-order `index` in fixture response → correctly re-indexed).

- [ ] **Step 1: Fixture server + providertest wiring → RED.**
- [ ] **Step 2: Implement wire.go / language_model.go / mistral.go / embedding.go per mapping.**
- [ ] **Step 3: Request-shape + stream-robustness + embedding tests → all GREEN.**
- [ ] **Step 4: Full check suite. Commit** — `feat: Mistral provider (chat completions + embeddings)`

---

### Task 5: Cohere provider (full implementation, v2 API)

**Files:**
- Create: `providers/cohere/cohere.go`, `providers/cohere/language_model.go`, `providers/cohere/wire.go`, `providers/cohere/embedding.go`
- Test: `providers/cohere/cohere_test.go`, `providers/cohere/embedding_test.go`

**Interfaces:**
- Consumes: same as Task 4.
- Produces: `cohere.New(opts ...Option) *Provider` (same Option trio), `Model(id)` (Name "cohere", `Capabilities{NativeJSON: true}`), `EmbeddingModel(id)` (MaxBatchSize 96). Key env `COHERE_API_KEY`; base default `https://api.cohere.com/v2`.

Wire mapping (Cohere v2):
- Request: `POST {base}/chat`, `Authorization: Bearer <key>`. Fields: `model`, `messages:[{role:"system"|"user"|"assistant"|"tool",...}]`, `tools:[{type:"function",function:{name,description,parameters}}]`, `response_format:{"type":"json_object","schema":<schema>}` (schema included when provided — Cohere supports it), `max_tokens`, `temperature`, `p` (Cohere's name for top_p), `stop_sequences`, `stream`.
- Messages: user/system content as string (text parts concatenated; image parts unsupported → return a descriptive error, Cohere v2 chat is text-only in this integration). Assistant tool calls: `{role:"assistant", tool_calls:[{id,type:"function",function:{name,arguments:<string>}}]}`. Tool results: `{role:"tool", tool_call_id, content:<JSON-marshaled result string>}` — one message per ToolResultPart; IsError has no slot → comment.
- ToolChoice: Cohere v2 has no tool_choice field for auto (default) — `required`/specific-tool are approximated: `ToolChoiceRequired` → send as-is with tools (documented comment: Cohere decides), `ToolChoiceNone` → omit tools, `ToolChoiceTool` → send only that one tool def (closest expressible semantics; comment each decision).
- Response: `{message:{content:[{type:"text",text}...], tool_calls:[{id,function:{name,arguments}}]}, finish_reason, usage:{tokens:{input_tokens,output_tokens}}}`. finish_reason: `COMPLETE`→stop, `MAX_TOKENS`→length, `TOOL_CALL`→tool-calls, `STOP_SEQUENCE`→stop, else other. Usage: input+output, TotalTokens = sum.
- Streaming: SSE `data:` events each carrying `{"type": ...}`: `content-delta` → TextDelta from `delta.message.content.text`; `tool-call-start` → note id/name from `delta.message.tool_calls` (begin accumulating; emit ToolCallDelta with the name); `tool-call-delta` → ToolCallDelta from `delta.message.tool_calls.function.arguments`; `tool-call-end` → ToolCallEnd with accumulated args (empty → `"{}"`); `message-end` → capture `delta.finish_reason` + `delta.usage.tokens`, then emit the single FinishPart. Ignore `message-start`, `content-start`, `content-end`, `tool-plan-delta`. Truncation rule per Global Constraints (stream ends without message-end).
- Errors: non-2xx → `ai.NewAPICallError`, message from `{"message":...}` when parseable.
- Embeddings: `POST {base}/embed` `{"model","texts":[...],"input_type":"search_document","embedding_types":["float"]}` → `embeddings.float` (`[][]float64`, in input order); usage from `meta.billed_units.input_tokens` → `Usage{InputTokens: n, TotalTokens: n}`. MaxBatchSize 96.

Tests: fixture server speaking Cohere v2 wire format for the six providertest scenarios (+ Cancel) → `providertest.Run(t, ...{ProviderName: "cohere"})`; request-shape assertions: `p` field name for top_p, `response_format` with schema, tool-result message shape, ToolChoiceTool → single tool def sent; stream-robustness both cases; embedding test asserting `input_type` and `embedding_types` in the request and float vectors + usage in the result.

- [ ] **Step 1: Fixture server + providertest wiring → RED.**
- [ ] **Step 2: Implement per mapping.**
- [ ] **Step 3: Request-shape + stream-robustness + embedding tests → GREEN.**
- [ ] **Step 4: Full check suite. Commit** — `feat: Cohere provider (v2 chat + embed)`

---

### Task 6: Docs — README provider table + spec roadmap tick

**Files:**
- Modify: `README.md` (feature table gains the nine new providers; roadmap table marks wave 2 shipped; quickstart unchanged)
- Modify: `docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md` (Provider waves table: annotate wave 2 rows "(shipped)")

**Interfaces:**
- Consumes: the shipped provider list from Tasks 2–5 (exact package paths and embedding support per the tables in Tasks 2–3 and the Produces blocks of Tasks 4–5).

README feature-table facts to encode: language models for all nine; embeddings ONLY for together, fireworks, mistral, cohere (plus existing openai, google); NativeJSON true for all presets and cohere, `json_object`-only for mistral (footnote), tool-mode for anthropic (existing); perplexity footnote: no tool support on the live API.

- [ ] **Step 1: Update both tables + footnotes.** Verify every claim against the code (grep for `EmbeddingModel`, `NativeJSON`).
- [ ] **Step 2: `go build ./... && go test ./...` (docs-only change, still verify). Commit** — `docs: wave 2 provider roster`

---

## Self-Review Notes

- **Spec coverage:** spec's wave-2 row = Groq, xAI, DeepSeek, Together, Fireworks, Cerebras, Perplexity (presets, T2–T3) + Mistral, Cohere (own APIs, T4–T5). Extraction of the compat base (T1) is the spec's "openai package doubles as the OpenAI-compatible base" made concrete (moved to `internal/` so presets can share it without exporting new public API). Docs T6. Azure/Vertex/Bedrock are wave 3 — out of scope here.
- **Type consistency:** `openaicompat.Config{Name, APIKey, BaseURL, HTTPClient, NativeJSON, EmbedBatch}` and `NewLanguageModel`/`NewEmbeddingModel`/`compattest.NewFixtureServer`/`Server.Requests`/`Server.AuthHeaders` used identically in T1–T3. Preset public surface (`Option`, `WithAPIKey`, `WithBaseURL`, `WithHTTPClient`, `New`, `Model`, optional `EmbeddingModel`) identical across T2–T5.
- **Placeholder scan:** the one intentionally-flagged placeholder block in Task 2's test template is explicitly instructed to be replaced by the `AuthHeaders()` assertion — no TBDs remain.
- **Contract guard:** providertest modification is forbidden by Global Constraints; Task 1 Step 3 forbids weakening existing openai tests — the two guardrails that keep the refactor honest.
