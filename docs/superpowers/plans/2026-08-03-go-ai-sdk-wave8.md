# go-ai-sdk Wave 8 Implementation Plan (Media Provider Roster)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the documented media-provider gap vs the Vercel AI SDK: Fal + Replicate + Luma (image generation), Deepgram (transcription), LMNT + Hume (speech). Then finalize v0.1.0 (CHANGELOG date + docs matrices).

**Architecture:** Six standalone provider packages implementing the existing `provider.{ImageModel,SpeechModel,TranscriptionModel}` interfaces. Three image providers introduce a shared URL-fetch step (the API returns hosted URLs; the provider downloads the bytes) — one tiny shared helper in `internal/fetchimage`. Luma adds a poll-until-complete loop.

**Tech Stack:** Go 1.26, stdlib only.

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- Providers NEVER retry (ai core retries); non-2xx → `ai.NewAPICallError`; ctx cancellation passthrough; `internal/imagesniff` for MediaType when the API doesn't report one.
- ProviderOptions: same namespaced raw-wire-key merge convention (each provider reads its own name key; JSON body merge; covering test per provider).
- Existing tests stay green; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit; providertest untouched.
- Docs discipline: every new provider gets a docs/providers page + all three matrix sites updated together (see the drift comments at each site).
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: `internal/fetchimage` + Fal + Replicate (image)

**Files:**
- Create: `internal/fetchimage/fetchimage.go` (+test), `providers/fal/fal.go` (+test), `providers/replicate/replicate.go` (+test)

**Interfaces:**

```go
// internal/fetchimage
// Fetch downloads an image URL (ctx-aware, using client or http.DefaultClient),
// returning bytes + MediaType (response Content-Type if image/*, else sniffed
// via imagesniff). Non-2xx → error including status and up to 1KB of body.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, string, error)

// providers/fal — Fal (fal.ai) image generation via the synchronous fal.run endpoint.
func New(opts ...Option) *Provider // WithAPIKey (env FAL_API_KEY, fallback env FAL_KEY), WithBaseURL (default https://fal.run), WithHTTPClient
func (p *Provider) ImageModel(id string) provider.ImageModel // e.g. "fal-ai/flux/schnell"

// providers/replicate — Replicate image generation via sync-mode predictions.
func New(opts ...Option) *Provider // WithAPIKey (env REPLICATE_API_TOKEN), WithBaseURL (default https://api.replicate.com), WithHTTPClient
func (p *Provider) ImageModel(id string) provider.ImageModel // e.g. "black-forest-labs/flux-schnell"
```

Wire mappings:
- **Fal**: `POST {base}/{modelID}` header `Authorization: Key <key>`; body `{"prompt":..., "num_images":N(omit 0), "image_size":Size(omit ""), "seed":*Seed(omit nil)}` + AspectRatio set → error `"fal: aspect ratio is not supported; use Size"`. ProviderOptions["fal"] merged top-level. Response `{"images":[{"url":..., "content_type":...}]}` → Fetch each URL (MediaType: content_type if set, else Fetch's); also accept `data:` URLs inline (base64 decode, no HTTP). Empty images → error.
- **Replicate**: `POST {base}/v1/models/{modelID}/predictions` headers `Authorization: Bearer <key>`, `Prefer: wait` (sync mode); body `{"input":{"prompt":..., "num_outputs":N(omit 0), "aspect_ratio":AspectRatio(omit ""), "seed":*Seed(omit nil)}}` + Size set → error `"replicate: size is not supported; use AspectRatio"`. ProviderOptions["replicate"] merged into the INPUT object (document divergence: replicate options are model inputs). Response `{"status":..., "output":...}` where output is a string URL or array of string URLs; status != "succeeded" → error including status + error field when present. Fetch URLs.

Tests per provider: request-shape (auth header, body incl. omissions + provider-options merge), URL-fetch happy path (fixture serves both endpoints), data-URL path (fal), empty/failed-status errors, 401 → APICallError, ctx cancellation. fetchimage: content-type vs sniff fallback, non-2xx error.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: Fal and Replicate image providers`

---

### Task 2: Luma (image, polling)

**Files:**
- Create: `providers/luma/luma.go` (+test)

**Interfaces:**

```go
// providers/luma — Luma Dream Machine image generation (async: create + poll).
func New(opts ...Option) *Provider // WithAPIKey (env LUMA_API_KEY), WithBaseURL (default https://api.lumalabs.ai), WithHTTPClient, WithPollInterval(d time.Duration) (default 500ms; test hook)
func (p *Provider) ImageModel(id string) provider.ImageModel // e.g. "photon-1"
```

Wire: `POST {base}/dream-machine/v1/generations/image` header `Authorization: Bearer <key>`; body `{"prompt":..., "model":modelID, "aspect_ratio":AspectRatio(omit "")}` + Size set → error (use AspectRatio); N>1 → error `"luma: multiple images per call are not supported"` (API generates one). ProviderOptions["luma"] top-level merge. Response `{"id":...}`. Poll `GET {base}/dream-machine/v1/generations/{id}` every PollInterval (ctx-aware sleep) until `state` == "completed" (→ `assets.image` URL → fetchimage.Fetch) or "failed" (→ error incl. failure_reason). Seed unsupported → ignored with comment.

Tests: create+poll happy path (fixture: 2 pending polls then completed), failed state, ctx cancellation mid-poll, request shapes, provider-options merge, N>1 error, Size error.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: Luma image provider`

---

### Task 3: Deepgram (transcription)

**Files:**
- Create: `providers/deepgram/deepgram.go` (+test)

**Interfaces:**

```go
// providers/deepgram — Deepgram speech-to-text.
func New(opts ...Option) *Provider // WithAPIKey (env DEEPGRAM_API_KEY), WithBaseURL (default https://api.deepgram.com), WithHTTPClient
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel // e.g. "nova-3"
```

Wire: `POST {base}/v1/listen?model={id}&smart_format=true` (+`&language={Language}` when set) header `Authorization: Token <key>`, `Content-Type: {MediaType or application/octet-stream}`; body = raw audio bytes. TranscriptionCall.Prompt unsupported → ignored with comment. ProviderOptions["deepgram"] merged as EXTRA QUERY PARAMETERS (values via fmt.Sprint; document divergence — Deepgram options are query params). Response `{"metadata":{"duration":...}, "results":{"channels":[{"alternatives":[{"transcript":..., "words":[{"word":...,"start":...,"end":...}]}]}]}}` → Text = first channel/alternative transcript; words → TranscriptSegments; DurationSec from metadata.duration; Language = detected_language when present else "".

Tests: request shape (query params incl. language + provider-options params, auth header, raw body content-type), response mapping (words→segments, duration), empty results → error, 401 → APICallError, ctx cancellation.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: Deepgram transcription provider`

---

### Task 4: LMNT + Hume (speech)

**Files:**
- Create: `providers/lmnt/lmnt.go` (+test), `providers/hume/hume.go` (+test)

**Interfaces:**

```go
// providers/lmnt — LMNT text-to-speech.
func New(opts ...Option) *Provider // WithAPIKey (env LMNT_API_KEY), WithBaseURL (default https://api.lmnt.com), WithHTTPClient
func (p *Provider) SpeechModel(id string) provider.SpeechModel // e.g. "blizzard"

// providers/hume — Hume (Octave) text-to-speech.
func New(opts ...Option) *Provider // WithAPIKey (env HUME_API_KEY), WithBaseURL (default https://api.hume.ai), WithHTTPClient
func (p *Provider) SpeechModel(id string) provider.SpeechModel // model id currently unused by the API; kept for interface symmetry (doc note)
```

Wire mappings:
- **LMNT**: `POST {base}/v1/ai/speech/bytes` header `X-API-Key: <key>`; JSON body `{"voice":Voice(default "leah"), "text":Text, "model":modelID, "format":OutputFormat(default "mp3"), "language":Language(omit "")}` + Speed non-nil → `"speed":*Speed`. ProviderOptions["lmnt"] top-level merge. Response = raw audio bytes; MediaType from format: mp3→audio/mpeg, wav→audio/wav, else application/octet-stream.
- **Hume**: `POST {base}/v0/tts` header `X-Hume-Api-Key: <key>`; body `{"utterances":[{"text":Text} (+"voice":{"name":Voice} when Voice != "")], "format":{"type":OutputFormat(default "mp3")}}`. Speed non-nil → utterance `"speed":*Speed`. Language unsupported → ignored (comment). ProviderOptions["hume"] top-level merge. Response `{"generations":[{"audio":"<base64>"}]}` → decode; empty generations/audio → error. MediaType per format (mp3/wav/pcm → audio/mpeg, audio/wav, audio/pcm).

Tests per provider: request shapes (headers, defaults, Speed/Language handling, options merge), audio decode (hume base64; lmnt raw), MediaType mapping, empty-audio errors, 401 → APICallError, ctx cancellation.

- [ ] **Step 1: TDD → implement → pass. Full check suite. Commit** — `feat: LMNT and Hume speech providers`

---

### Task 5: Docs + matrices + v0.1.0 finalization

**Files:**
- Create: `docs/providers/{fal,replicate,luma,deepgram,lmnt,hume}.md`
- Modify: `docs/providers/README.md` (matrix + links, 22 providers), `README.md` (matrices + "not yet implemented" list now empty or reduced — Vercel's remaining exotic providers, if any, stay listed honestly), `docs/core/media.md` (matrices), `docs/getting-started.md` (env table +6), `docs/README.md` (tree), `CHANGELOG.md` (add wave-8 entries under Unreleased; do NOT date/tag — that happens post-merge), `docs/troubleshooting.md` (auth bullet additions), spec waves table.

**Rules:** same page template + verification discipline as the existing provider pages (construction snippet compile-verified, claims source-verified, footers, all three matrix sites updated together per the drift comments, table cell counts).

- [ ] **Step 1: Six pages + all matrix/table updates, verified. Full check suite. Commit** — `docs: media provider roster (fal, replicate, luma, deepgram, lmnt, hume)`

---

## Self-Review Notes

- **Roster ledger:** closes fal/replicate/luma/deepgram/lmnt/hume — the six named in our docs' not-yet-included list. Vercel providers still absent afterward (assemblyai, gladia, revai transcription; others marginal) get an honest mention in the README list — documented, not hidden.
- **Type consistency:** all six implement existing provider interfaces; `fetchimage.Fetch` produced T1, consumed T1/T2; poll hook `WithPollInterval` is luma-only.
- **ProviderOptions divergences documented per provider** (replicate→input object; deepgram→query params) mirroring the established doc convention.
