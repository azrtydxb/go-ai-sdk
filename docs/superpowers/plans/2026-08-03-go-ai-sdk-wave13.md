# go-ai-sdk Wave 13 Implementation Plan (MCP extensions + provider fleet)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the MCP client beyond tools (resources, resource templates, prompts, completions, elicitation, token-provider auth, retries) and add the AI SDK 6 provider fleet: 8 openai-compat presets, Voyage (embed+rerank), Mixedbread (rerank), Cartesia (speech), Prodia + Black Forest Labs (image), and an AI Gateway provider.

**Architecture:** MCP extensions are additive methods on `mcp.Client` reusing the existing `call`/`recvLoop` machinery, plus a `TokenProvider` hook on the HTTP transport for per-request auth. The preset fleet reuses `internal/openaicompat` verbatim (config-only). Rerank/speech/image providers follow the Cohere-rerank / LMNT-speech / Fal-sync + Luma-poll templates. Every new provider is registry-compatible by method-set alone (no registry changes).

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only; ADDITIVE only on existing exported surfaces.
- Providers never retry non-2xx (MCP retries are a separate, opt-in transport concern — see Task 3); non-2xx → `ai.NewAPICallError`; ctx passthrough; ProviderOptions namespaced raw wire keys merged last (options win).
- All new providers fixture-tested only; every provider page carries the live-testing note; `docs/providers/README.md` matrix (+ README.md + docs/core/media.md — the three canonical copies) updated together.
- `provider/providertest` and `internal/openaicompat` internals are reused, not modified (openaicompat may gain Config fields ONLY if a preset genuinely needs a new quirk — document; prefer ProviderOptions).
- MCP protocol version stays `2025-03-26`; new client capabilities declared in `initialize` only when actually implemented (elicitation).
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` + `-race` on `./mcp/...` when touched.
- Commits conventional, trailer:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: MCP resources + resource templates + prompts

**Files:**
- Create: `mcp/resources.go`, `mcp/resources_test.go`, `mcp/prompts.go`, `mcp/prompts_test.go`
- Modify: `mcp/client.go` (capability storage on Client — see below)

**Interfaces (Produces):**

Store the server's advertised capabilities from `initialize`'s result so the new methods can return a clear "server does not support X" error instead of a raw RPC error. Add to `Client` (unexported field set in Initialize): `serverCaps map[string]json.RawMessage` (the raw `capabilities` object). Add:
```go
// Resources
type Resource struct {
	URI         string
	Name        string
	Title       string
	Description string
	MimeType    string
}
type ResourceTemplate struct {
	URITemplate string // RFC 6570
	Name        string
	Title       string
	Description string
	MimeType    string
}
type ResourceContents struct {
	URI      string
	MimeType string
	Text     string // set for text resources
	Blob     []byte // set (decoded from base64) for binary resources
}
func (c *Client) ListResources(ctx context.Context) ([]Resource, error)                 // resources/list, cursor-paginated
func (c *Client) ListResourceTemplates(ctx context.Context) ([]ResourceTemplate, error) // resources/templates/list, paginated
func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContents, error) // resources/read

// Prompts
type Prompt struct {
	Name        string
	Title       string
	Description string
	Arguments   []PromptArgument
}
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}
type PromptMessage struct {
	Role    string        // "user"/"assistant"
	Content []PromptPart  // text and resource parts flattened
}
type PromptPart struct {
	Type     string // "text" | "resource" | "image" | "audio"
	Text     string
	Resource *ResourceContents // for embedded resource parts
	Data     []byte            // decoded, for image/audio
	MimeType string
}
func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error)                              // prompts/list, paginated
func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (description string, messages []PromptMessage, error) // prompts/get
```
Rules: pagination follows `nextCursor` like ListTools (extract a shared `paginate` helper in client.go if clean — `ListTools` becomes a caller; do NOT change ListTools' behavior). Base64 `blob` decoded to `Blob`; `text` → `Text`. Capability gate: if `serverCaps["resources"]`/`["prompts"]` absent, return a typed `*CapabilityError{Capability string}` (new, in client.go) BEFORE sending the request. Unknown content-part types in prompt messages preserved as `Type` with raw Text empty (no error).

**Tests** (pipeTransport-based, like client_test.go): list+read resources incl. pagination + binary blob decode; templates list; capability-absent → CapabilityError without a wire send (assert the fake server received nothing); prompts list with arguments; get prompt with args producing multi-part messages incl. embedded resource; error passthrough (RPCError).

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `feat(mcp): resources, resource templates, and prompts`

---

### Task 2: MCP completions + elicitation

**Files:**
- Create: `mcp/completion.go`, `mcp/completion_test.go`, `mcp/elicitation.go`, `mcp/elicitation_test.go`
- Modify: `mcp/client.go` (declare elicitation capability in Initialize; add server-initiated-request dispatch), `mcp/jsonrpc.go` (recvLoop must dispatch server→client requests — currently dropped)

**Interfaces (Produces):**
```go
// Completions (argument autocompletion) — completion/complete
type CompletionRef struct {
	Type string // "ref/prompt" or "ref/resource"
	Name string // prompt name (ref/prompt)
	URI  string // resource template URI (ref/resource)
}
type Completion struct {
	Values  []string
	Total   int  // server's total count when provided
	HasMore bool
}
func (c *Client) Complete(ctx context.Context, ref CompletionRef, argName, argValue string) (*Completion, error)
```
**Elicitation** (server → client request `elicitation/create`): the server can ask the client to gather user input mid-session. This requires recvLoop to dispatch server-initiated requests, which the current transport-limited HTTP path cannot receive (only stdio can). Implement the dispatch generically; document that HTTP won't exercise it (no server-initiated channel).
```go
type ElicitationRequest struct {
	Message         string
	RequestedSchema json.RawMessage // JSON schema of the requested object
}
type ElicitationResult struct {
	Action  string         // "accept" | "decline" | "cancel"
	Content map[string]any // set when Action == "accept"
}
// ElicitationHandler is called when the server sends elicitation/create.
// Nil handler → the client auto-responds with action "decline".
type ElicitationHandler func(ctx context.Context, req ElicitationRequest) (ElicitationResult, error)
// SetElicitationHandler installs the handler; call before Initialize so the
// capability is declared. Declaring elicitation capability in initialize is
// gated on a handler being set.
func (c *Client) SetElicitationHandler(h ElicitationHandler)
```
recvLoop change (jsonrpc.go): a message with a non-nil `id` AND a `method` is a server→client REQUEST (today only responses have ids and are matched to pending; requests are dropped). Route these to a `serverRequestHandler` on Client that switches on method: `elicitation/create` → the handler (or auto-decline) → send an `rpcResponse` with the same id. Unknown server methods → respond with JSON-RPC error `-32601 Method not found`. Keep this dispatch on the recvLoop goroutine but run the handler in a new goroutine so a slow handler doesn't block response reading (the response send is serialized via sendMu like any write). Initialize declares `capabilities.elicitation = {}` only when a handler was set.

**Tests:** completion/complete happy path (prompt ref + resource ref, values/total/hasMore); Complete capability-gated if server lacks `completions` cap → CapabilityError. Elicitation: a pipeTransport test where the fake server sends an `elicitation/create` request after initialize, asserts the client invokes the handler and sends back the accept/decline response with matching id; nil-handler auto-decline; unknown server method → -32601; handler-returns-error → decline-with-... (define: error → respond action cancel). Initialize declares the capability only with a handler set (assert both ways). Race test: server request arrives concurrent with a client call.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `feat(mcp): completions and elicitation (server-initiated request dispatch)`

---

### Task 3: MCP token-provider auth + retries on the HTTP transport

**Files:**
- Modify: `mcp/http.go` (TokenProvider option + retry option), `mcp/http_test.go`
- Create: `mcp/http_auth_test.go` (focused auth/retry tests)

**Interfaces (Produces):**
```go
// TokenProvider supplies a bearer token per request, enabling refresh/rotation.
// Returned token is sent as "Authorization: Bearer <token>" unless Header is set.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}
// TokenProviderFunc adapts a func.
type TokenProviderFunc func(ctx context.Context) (string, error)

// HTTPOption configures the Streamable HTTP transport.
type HTTPOption func(*httpTransport)
func WithTokenProvider(tp TokenProvider) HTTPOption           // per-request Authorization; overrides a static Authorization header
func WithAuthHeader(name string) HTTPOption                   // send the token under a custom header instead of Authorization/Bearer
func WithHTTPRetry(maxRetries int) HTTPOption                 // retry idempotent POSTs on 429/503/network error w/ backoff; default 0 (off)
func WithHTTPClientOpt(c *http.Client) HTTPOption

// New signature (additive: keep the old constructor, add an option-taking one)
func NewStreamableHTTPTransportWithOptions(url string, opts ...HTTPOption) Transport
```
Keep `NewStreamableHTTPTransport(url, headers)` unchanged (delegates to the options form with a static-headers option `withStaticHeaders(headers)`). TokenProvider is called on every Send; its result sets Authorization (or the custom header) fresh each request — this is the refresh story. Retry: only when `maxRetries > 0`, retry on HTTP 429/503 and connection errors with capped exponential backoff honoring `Retry-After` when present; respect ctx; never retry once any response bytes have been consumed from an SSE stream (at-most-once for streaming). A retried request re-invokes TokenProvider (fresh token on 401? — 401 is NOT retried by default; document: token refresh is the provider's job, transport only retries transient 429/503/network).

**Tests:** TokenProvider called per-request (2 sends → 2 token calls, tokens can differ); custom auth header; static-headers backward-compat unchanged; retry on 429 then 200 (assert attempt count + Retry-After honored via a tiny duration); retry exhausted → error; no-retry on 400; ctx cancel during backoff; SSE response not retried mid-stream. -race.

- [ ] **Step 1: Failing tests → implement → green. Full check suite (-race on mcp). Commit** — `feat(mcp): token-provider auth and transient retry on the HTTP transport`

---

### Task 4: openai-compat preset fleet (8 providers)

**Files:**
- Create per provider (mirror providers/cerebras exactly — provider file + test): `providers/moonshot/`, `providers/qwen/`, `providers/minimax/`, `providers/deepinfra/`, `providers/huggingface/`, `providers/baseten/`, `providers/lmstudio/`, `providers/nvidia/`

**Config per provider (all `Name`, `APIKey` env, `BaseURL`, plus quirks):**
- **moonshot**: env `MOONSHOT_API_KEY`, base `https://api.moonshot.ai/v1`, NativeJSON true. Model + (Moonshot has embeddings? no — Model only).
- **qwen** (Alibaba DashScope, OpenAI-compatible mode): env `DASHSCOPE_API_KEY`, base `https://dashscope-intl.aliyuncs.com/compatible-mode/v1`, NativeJSON true, MaxTokensParam `"max_tokens"`. Model + EmbeddingModel (EmbedBatch 10, `text-embedding-v3`).
- **minimax**: env `MINIMAX_API_KEY`, base `https://api.minimax.io/v1`, NativeJSON true, MaxTokensParam `"max_tokens"`. Model only.
- **deepinfra**: env `DEEPINFRA_API_KEY`, base `https://api.deepinfra.com/v1/openai`, NativeJSON true. Model + EmbeddingModel (EmbedBatch 1024).
- **huggingface** (router, OpenAI-compatible): env `HF_TOKEN`, base `https://router.huggingface.co/v1`, NativeJSON false (router models vary — conservative), MaxTokensParam `"max_tokens"`. Model only.
- **baseten**: env `BASETEN_API_KEY`, base `https://inference.baseten.co/v1`, NativeJSON true. Model + EmbeddingModel (EmbedBatch 1).
- **lmstudio** (local): env `LMSTUDIO_API_KEY` (default no key — local; New defaults apiKey "" and base `http://localhost:1234/v1`), NativeJSON true. Model + EmbeddingModel (EmbedBatch 1). Auth header still sent as Bearer even if empty (LM Studio ignores it) — verify empty key doesn't break the header set; if openaicompat sends `Authorization: Bearer ` with empty, that's fine for local. Document local-first.
- **nvidia** (NIM): env `NVIDIA_API_KEY`, base `https://integrate.api.nvidia.com/v1`, NativeJSON true. Model + EmbeddingModel (EmbedBatch 1).

Each: `Provider{apiKey, baseURL, httpClient}`, `WithAPIKey/WithBaseURL/WithHTTPClient`, `New`, `Model(id)`, plus `EmbeddingModel(id)` where noted. Tests mirror cerebras_test.go: TestConformance via compattest.NewFixtureServer (the fixture server takes the provider name — verify compattest.NewFixtureServer accepts an arbitrary name; if it hardcodes known names, extend it minimally per its sanctioned-extension rules OR use a generic name and assert separately), TestDefaults (base URL, ProviderName, NativeJSON), TestAuthHeaderAndModelSent. Embedding-bearing presets add a TestEmbeddingRequestShape.

- [ ] **Step 1: All 8 presets + tests → green. Full check suite. Commit** — `feat: OpenAI-compatible presets — Moonshot, Qwen, MiniMax, DeepInfra, HuggingFace, Baseten, LM Studio, NVIDIA NIM`

---

### Task 5: Voyage (embeddings + rerank) + Mixedbread (rerank)

**Files:**
- Create: `providers/voyage/{voyage.go,embedding.go,rerank.go,wire.go}` (+tests), `providers/mixedbread/{mixedbread.go,rerank.go}` (+tests)

**Voyage** (env `VOYAGE_API_KEY`, base `https://api.voyageai.com/v1`, `Authorization: Bearer`):
- `EmbeddingModel(id)` (e.g. "voyage-3"): POST `/embeddings` `{"model","input":[...],"input_type"?}` → `{"data":[{"embedding":[...],"index"}],"usage":{"total_tokens"}}`. MaxBatchSize 128. Implement as `provider.EmbeddingModelWithOptions` (ProviderOptions["voyage"] merged — e.g. input_type, output_dimension). Mirror cohere/embedding.go structure.
- `RerankingModel(id)` (e.g. "rerank-2"): POST `/rerank` `{"model","query","documents":[...],"top_k"?}` → `{"data":[{"index","relevance_score"}],"usage":{"total_tokens"}}` → RankedDocument. Usage.TotalTokens from usage. Mirror cohere/rerank.go.
**Mixedbread** (env `MXBAI_API_KEY`, base `https://api.mixedbread.com/v1`, Bearer):
- `RerankingModel(id)` (e.g. "mixedbread-ai/mxbai-rerank-large-v1"): POST `/rerank` `{"model","query","input":[...],"top_k"?,"return_input":false}` → `{"data":[{"index","score"}]}` → RankedDocument. (Note the field names: `input` not `documents`, `score` not `relevance_score`.)

Tests: embed request/response shape + batch size + options merge (voyage); rerank shapes both providers (field-name differences asserted), top_n/top_k omitted when 0, order preserved, 401/429, ctx. Registry lookup works (EmbeddingModel/RerankingModel provider interfaces satisfied).

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: Voyage (embeddings + rerank) and Mixedbread (rerank)`

---

### Task 6: Cartesia (speech) + Prodia + Black Forest Labs (image)

**Files:**
- Create: `providers/cartesia/{cartesia.go,speech.go}` (+test), `providers/prodia/{prodia.go,image.go}` (+test), `providers/bfl/{bfl.go,image.go}` (+test)

**Cartesia** (env `CARTESIA_API_KEY`, base `https://api.cartesia.ai`, headers `Authorization: Bearer <key>` + `Cartesia-Version: 2024-11-13`): `SpeechModel(id)` (e.g. "sonic-2"): POST `/tts/bytes` `{"model_id":id,"transcript":Text,"voice":{"mode":"id","id":Voice},"output_format":{"container":<from OutputFormat, default "mp3">,"encoding":"mp3"/"pcm_f32le"...,"sample_rate":44100},"language":Language(omit "")}` + ProviderOptions["cartesia"] merged. Returns raw audio bytes; MediaType from container ("mp3"→audio/mpeg, "wav"→audio/wav, "raw"→application/octet-stream). Mirror lmnt/speech.go. Voice required — if empty, error (Cartesia needs a voice id).
**Prodia** (env `PRODIA_API_KEY`, base `https://inference.prodia.com/v2`, `Authorization: Bearer`): `ImageModel(id)`: POST `/job` (sync-ish; Prodia v2 returns the image inline on the job endpoint with Accept: image/jpeg) `{"type":"inference.<id>.txt2img"?, "config":{"prompt":Prompt, ...}}` — SIMPLIFY to Prodia's documented v2 shape: POST `/job` with `{"type": id, "config":{"prompt":...}}`, Accept `image/jpeg`, response body IS the image bytes → GeneratedImage{Data, MediaType:"image/jpeg"}. ProviderOptions merged into config. (Fixture-tested; live shape flagged uncertain in the doc.)
**Black Forest Labs** (env `BFL_API_KEY`, base `https://api.bfl.ai`, header `x-key: <key>`): `ImageModel(id)` (e.g. "flux-pro-1.1"): async poll — POST `/v1/<id>` `{"prompt":Prompt, "width"?, "height"? from Size "WxH", ...}` → `{"id","polling_url"}`; poll the returned `polling_url` (absolute URL) until `{"status":"Ready","result":{"sample":<url>}}` (fetch the sample URL for bytes) or `status` in {Error,Content Moderated,...} → error. WithPollInterval option (default 500ms). Mirror luma/image.go poll discipline; fetch bytes via internal/fetchimage.

Tests: cartesia speech (request shape incl. Cartesia-Version header, output_format mapping, media type, voice-required error); prodia (job POST shape, image bytes passthrough, 401); bfl (create+poll fixture sequence via pollBodies pattern, polling_url follow, Ready→sample fetch, error status, ctx cancel mid-poll, x-key header). 

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: Cartesia (speech), Prodia and Black Forest Labs (image)`

---

### Task 7: AI Gateway provider

**Files:**
- Create: `providers/gateway/{gateway.go,gateway_test.go}`

**Vercel AI Gateway** is an OpenAI-compatible routing endpoint that fronts many upstream models via `provider/model` slugs. Implement as an openaicompat preset with a twist: env `AI_GATEWAY_API_KEY`, base `https://ai-gateway.vercel.sh/v1` (WithBaseURL overridable), NativeJSON false (upstreams vary — conservative default; document that ProviderOptions/model choice governs). `Model(id)` where id is a gateway slug like `"openai/gpt-4o"` or `"anthropic/claude-3-5-sonnet"` — passed through as the `model` field verbatim. Model + EmbeddingModel (EmbedBatch 1 — conservative). Auth `Authorization: Bearer`. Also support an `WithAPIKey` fallback to env `VERCEL_OIDC_TOKEN`? No — keep to `AI_GATEWAY_API_KEY` only, document the OIDC path as out of scope.
Note the model-id-contains-slash interaction with Registry.splitID (splits on FIRST colon, not slash — so `gateway:openai/gpt-4o` → provider "gateway", model "openai/gpt-4o" works fine; verify and add a registry test).

Tests: TestConformance (compattest fixture with name "gateway"), TestDefaults, TestAuthHeaderAndModelSent asserting the slug passes through as `model`, TestEmbeddingRequestShape, and a Registry round-trip test with a slash-bearing model id.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: Vercel AI Gateway provider`

---

### Task 8: Wave-13 docs + CHANGELOG

**Files:**
- Create: `docs/providers/{moonshot,qwen,minimax,deepinfra,huggingface,baseten,lmstudio,nvidia,voyage,mixedbread,cartesia,prodia,bfl,gateway}.md` (14 pages, mirror an existing preset/rerank/speech/image page's structure)
- Modify: `mcp.md` (resources/prompts/completions/elicitation/token-provider auth/retries — a major expansion; update the "tools-only" limitations section — it's no longer tools-only; keep the transport-deviation notes accurate: HTTP still can't receive server-initiated elicitation), `docs/providers/README.md` (matrix +14 rows, construction table +14, provider bullets +14, count 25→39; the 3 canonical matrix copies: README.md + docs/core/media.md too), `docs/core/embeddings.md` (Voyage embed + rerank; Mixedbread rerank), `docs/core/media.md` (Cartesia speech; Prodia/BFL image matrices), `docs/getting-started.md` (env var rows +14), `README.md` (provider list + count), `docs/migrating-from-vercel-ai-sdk.md` (MCP extensions row → Shipped with the elicitation-over-HTTP caveat; provider-fleet row → Shipped; rerank row → Voyage/Mixedbread now shipped; update the "MCP is tools-only" section — retitle, it now covers resources/prompts/etc.), `CHANGELOG.md` (Wave 13 entries), `docs/README.md` (verify provider list/index).
- Verification discipline as prior waves: snippets compile-verified, claims grepped, matrix cell counts (39 rows × columns), links resolve, env var table complete.

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 13 — MCP extensions and 14 new providers`

---

## Self-Review Notes

- Provider count goes 25 → 39. The three canonical matrix copies (docs/providers/README.md, README.md, docs/core/media.md) must all reach 39 rows — grep the HTML-comment marker in providers/README.md and update all three.
- MCP is no longer "tools-only" — the migration doc's `### MCP is tools-only` section and the `#mcp-is-tools-only` anchor referenced from the delta table must both be updated (retitle to something like "MCP scope" and adjust the anchor + its referrer, or keep the anchor and rewrite the body; grep for the anchor's users first).
- Task 2's server-initiated-request dispatch is a genuine architecture change to recvLoop — the review must check it doesn't regress the response-matching path (a message with id+result is a response; id+method is a request; id+error is a response error — get the discrimination right) and is race-clean.
- Elicitation over the HTTP transport genuinely cannot work (no server→client channel) — ship the dispatch (exercised by stdio/pipe tests) and document the HTTP limitation honestly rather than pretending.
- openaicompat Config reuse: if any preset needs a NEW quirk flag not already in Config, that's a sanctioned openaicompat extension — document why in the task report; otherwise route quirks through ProviderOptions.
- Task ordering: 1 → 2 (2 depends on 1's capability-storage + CapabilityError) → 3 (independent of 1/2, but same files region as client — do after) → 4 → 5 → 6 → 7 → 8. Tasks 4-7 are mutually independent provider work.
- compattest.NewFixtureServer name handling: verify it accepts arbitrary provider names before writing 9 preset conformance tests against it; if it has an allowlist, that's a sanctioned test-helper extension (add the new names) — note in the Task 4 report.
