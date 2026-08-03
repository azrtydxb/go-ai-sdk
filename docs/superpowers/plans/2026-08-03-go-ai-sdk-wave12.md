# go-ai-sdk Wave 12 Implementation Plan (video, websocket, streaming STT, realtime, translate, files & skills)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** AI SDK 6 modality parity: `provider.VideoModel` + `ai.GenerateVideo` (Luma/Fal/Replicate), a stdlib RFC 6455 WebSocket client (`internal/websocket`), streaming transcription (`ai.StreamTranscribe` — Deepgram live + OpenAI realtime transcription), audio translation (`ai.Translate` — OpenAI), a minimal OpenAI realtime voice session, and file/skill upload with file references in prompts (OpenAI + Anthropic).

**Architecture:** Video mirrors the image surface exactly (interface in `provider/`, retry orchestration in `ai/`, Luma poll / Fal sync / Replicate `Prefer: wait`, result bytes fetched from returned URLs). `internal/websocket` follows `internal/sse`'s conventions (minimal exported surface, stdlib only) and underpins the three live features. Streaming transcription is a new provider interface with a bidirectional stream object (Send audio / iterate events); ai.StreamTranscribe is validation + passthrough (no retry on live connections). Files extend `provider.FilePart` with reference fields plus a `FileStore` provider interface.

**Tech Stack:** Go 1.26, stdlib only (net, crypto/sha1, encoding/base64 for websocket).

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only; ADDITIVE only on existing exported surfaces (FilePart gains fields; no field changes/removals).
- Providers never retry; non-2xx → `ai.NewAPICallError`; ctx passthrough; ProviderOptions namespaced raw wire keys merged last (options win) — for query-param providers (deepgram) merged as extra query params as today.
- All new providers/endpoints fixture-tested only; every touched provider doc page carries the live-testing note. WebSocket tests run against in-test raw `net.Listener` servers (no external deps).
- `provider/providertest` untouched.
- Scope rulings recorded here (flag in migration doc): StreamTranslate is NOT shipped — no provider we target offers live audio translation over WS; `ai.Translate` (REST, OpenAI `/v1/audio/translations`, translate-to-English) ships instead, and the migration doc's wave-12 row is updated to say exactly that. uploadSkill ships as an Anthropic skills client only (beta API); OpenAI has no equivalent stable API — documented.
- Full check suite per commit: `go vet ./... && go build ./... && go test ./... && gofmt -l .` + `-race` on touched `ai`/`internal/websocket` packages.
- Commits conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: provider.VideoModel + ai.GenerateVideo + Luma/Fal/Replicate video

**Files:**
- Create: `provider/video.go`, `ai/generate_video.go`, `ai/generate_video_test.go`, `providers/luma/video.go` (+test), `providers/fal/video.go` (+test), `providers/replicate/video.go` (+test)
- Modify: `ai/registry.go` (+VideoModelProvider + Registry.VideoModel, mirroring the six existing lookups), `ai/aitest/mock.go` (+MockVideoModel, same shape as MockImageModel)

**Interfaces (Produces):**

`provider/video.go`:
```go
type VideoCall struct {
	Prompt          string
	AspectRatio     string  // e.g. "16:9"; empty = provider default
	Resolution      string  // e.g. "720p"; empty = provider default
	DurationSec     float64 // 0 = provider default
	ProviderOptions map[string]any
}

type GeneratedVideo struct {
	Data      []byte // the video bytes (providers download from result URLs)
	MediaType string // e.g. "video/mp4"
	URL       string // source URL when the provider returned one (may expire)
}

type VideoResponse struct {
	Videos []GeneratedVideo
	Raw    json.RawMessage
}

type VideoModel interface {
	GenerateVideos(ctx context.Context, call VideoCall) (*VideoResponse, error)
	ModelID() string
	ProviderName() string
}
```

`ai/generate_video.go` — mirror `ai/generate_image.go`'s skeleton exactly (validate ErrModelRequired/ErrPromptRequired → retry.Do → ExhaustedError→RetryError → zero-result guard):
```go
type GenerateVideoOpts struct {
	Model           provider.VideoModel
	Prompt          string
	AspectRatio     string
	Resolution      string
	DurationSec     float64
	MaxRetries      *int
	ProviderOptions map[string]any
}
type GenerateVideoResult struct {
	Video  provider.GeneratedVideo // Videos[0]
	Videos []provider.GeneratedVideo
}
func GenerateVideo(ctx context.Context, opts GenerateVideoOpts) (*GenerateVideoResult, error)
```

**Providers** (each: `(p *Provider) VideoModel(id string) provider.VideoModel`; reuse each package's existing auth/error helpers; ProviderOptions merge per package convention — replicate's nests under `input`):
- **Luma** (`providers/luma/video.go`): POST `{base}/dream-machine/v1/generations` `{"prompt", "model": id, "aspect_ratio"(omit ""), "resolution"(omit ""), "duration": "5s"-style string from DurationSec (omit 0)}` → `{"id","state"}`; poll GET `{base}/dream-machine/v1/generations/{id}` (reuse the package's existing poll/sleep discipline and WithPollInterval) until `state=="completed"` (→ `assets.video` URL) or `"failed"` (→ error w/ failure_reason); GET the asset URL for bytes (MediaType from response Content-Type, default `video/mp4`).
- **Fal** (`providers/fal/video.go`): sync POST `{base}/{modelID}` (same as image path pattern) `{"prompt", ...aspect_ratio/resolution/duration mapped as provider options keys — send only Prompt first-class; AspectRatio→"aspect_ratio" when set}` → response `{"video":{"url":...}}` (also accept `{"videos":[{"url"}]}`); fetch URL for bytes.
- **Replicate** (`providers/replicate/video.go`): POST predictions with `Prefer: wait` (same as image), input `{"prompt", "aspect_ratio"(omit)}` + options into `input`; output = string URL or []string URLs → fetch each.
- URL fetching: use the provider's own httpClient; non-2xx on fetch → APICallError. Check `internal/fetchimage` (existing helper for fetching image bytes) — if its shape fits, reuse/generalize it rather than duplicating; otherwise local helper per provider is acceptable IF fetchimage is image-specific (document the decision in the report).

**Tests** per provider: fixture flows (luma: create+poll sequence via the assemblyai-style pollBodies fixture pattern + asset download endpoint; fal/replicate: single round trip + download), request shapes (omitted-when-empty fields, ProviderOptions merge/nesting, auth headers), failed-state errors, 401/429 retryable classification, ctx cancel mid-poll. ai: validation, retry-then-success, empty-videos guard, MockVideoModel recording. Registry lookup test.

- [ ] **Step 1: Failing tests → implement → green. Full check suite. Commit** — `feat: video generation — provider.VideoModel, ai.GenerateVideo, Luma/Fal/Replicate`

---

### Task 2: internal/websocket — RFC 6455 client

**Files:**
- Create: `internal/websocket/websocket.go`, `internal/websocket/frame.go`, `internal/websocket/websocket_test.go`, `internal/websocket/frame_test.go`

**Interfaces (Produces):**
```go
package websocket

const (
	TextMessage   = 1
	BinaryMessage = 2
)

type DialOptions struct {
	Header          http.Header      // extra handshake headers (auth)
	TLSConfig       *tls.Config      // wss; nil = default
	NetDial         func(ctx context.Context, network, addr string) (net.Conn, error) // nil = &net.Dialer{}
	MaxMessageBytes int              // per-message cap; 0 = 16 MiB
}

// Dial performs the RFC 6455 client opening handshake against a ws:// or
// wss:// URL and returns an open connection.
func Dial(ctx context.Context, wsURL string, opts DialOptions) (*Conn, error)

type Conn struct{ /* unexported */ }

// Read returns the next complete data message (reassembling fragments).
// Control frames are handled internally: pings are answered with pongs,
// pongs are ignored, and a close frame completes the closing handshake and
// returns *CloseError. ctx cancels a blocked read (the connection is then
// unusable). Read must only be called from one goroutine.
func (c *Conn) Read(ctx context.Context) (messageType int, data []byte, err error)

// WriteText/WriteBinary send one masked data message (client role always
// masks). Safe for one writer goroutine concurrent with one reader.
func (c *Conn) WriteText(ctx context.Context, data []byte) error
func (c *Conn) WriteBinary(ctx context.Context, data []byte) error

// Close sends a close frame with the given code and reason (best effort),
// then closes the underlying connection. Idempotent.
func (c *Conn) Close(code int, reason string) error

type CloseError struct {
	Code   int
	Reason string
}
func (e *CloseError) Error() string

const (
	CloseNormal        = 1000
	CloseGoingAway     = 1001
	CloseProtocolError = 1002
	CloseAbnormal      = 1006 // never sent on the wire; synthesized on abrupt EOF
)
```
**Protocol requirements (binding):**
- Handshake: HTTP/1.1 GET with `Upgrade: websocket`, `Connection: Upgrade`, `Sec-WebSocket-Version: 13`, `Sec-WebSocket-Key` = base64 of 16 random bytes (crypto/rand); verify status 101 and `Sec-WebSocket-Accept` == base64(SHA1(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")); no extensions or subprotocols offered; non-101 → error including status and (bounded) body.
- Frames: client→server always masked with fresh crypto/rand mask per frame; server→client MUST be unmasked (masked server frame → protocol error close 1002). Support 7-bit/16-bit/64-bit payload lengths; reject control frames >125 bytes or fragmented; reassemble fragmented data messages (opcode 0 continuation); reject new data opcode mid-fragmentation and continuation with nothing to continue (1002). Interleaved control frames during fragmentation handled.
- MaxMessageBytes enforced across reassembled size → close 1009 + error.
- Close handshake: on receiving close, echo it (once) and return *CloseError from Read. Close() is idempotent and safe concurrent with Read (internal mutex on writes; conn close unblocks the reader).
- ctx plumbing: Dial honors ctx (dial + handshake deadlines); Read/Write use net.Conn deadlines derived from ctx (a background goroutine watching ctx.Done() calling SetDeadline is acceptable; document the one-reader/one-writer concurrency contract).
- UTF-8 validation of text messages: NOT performed (documented — callers of this internal package parse JSON, which fails naturally on garbage). No permessage-deflate.

**Tests** (against an in-test server on a raw net.Listener with a minimal server-side frame codec written in _test.go): handshake success incl. Accept verification; wrong Accept → error; non-101 → error w/ status; echo text + binary; fragmentation reassembly (2 and 3 fragments, with an interleaved ping); ping → automatic pong (assert server receives it); masked-server-frame → 1002; oversized message → 1009 error; 16-bit and 64-bit length boundary payloads (126, 65536 bytes); close handshake initiated by server → *CloseError with code/reason; Close idempotency; ctx cancel unblocks Read; wss via tls test server (httptest.NewTLSServer's listener or crypto/tls with a self-signed pair in-test); frame codec unit tests (mask round-trip, length encodings).

- [ ] **Step 1: Frame codec failing tests → implement frame.go → green. Commit** — `feat(internal): websocket frame codec`
- [ ] **Step 2: Dial/Conn failing tests → implement → green. Full check suite (-race on internal/websocket). Commit** — `feat(internal): RFC 6455 websocket client`

---

### Task 3: Streaming transcription — provider interface + ai.StreamTranscribe + Deepgram live + OpenAI realtime transcription

**Files:**
- Create: `provider/transcription_stream.go`, `ai/stream_transcribe.go`, `ai/stream_transcribe_test.go`, `providers/deepgram/live.go` (+test), `providers/openai/realtime_transcription.go` (+test)
- Modify: `ai/aitest/mock.go` (+MockStreamingTranscriptionModel with scripted event sequences)

**Interfaces (Produces):**

`provider/transcription_stream.go`:
```go
type StreamTranscriptionCall struct {
	MediaType       string  // e.g. "audio/pcm;rate=16000" — provider maps/validates
	Language        string
	SampleRate      int     // hint for raw-PCM providers; 0 = provider default
	ProviderOptions map[string]any
}

type TranscriptEvent struct {
	Text     string  // transcript delta or segment text
	Final    bool    // finalized segment vs interim hypothesis
	StartSec float64 // 0 when unknown
	EndSec   float64
}

// TranscriptionStream is a live bidirectional transcription session.
// One goroutine may Send audio while another ranges over Events.
type TranscriptionStream interface {
	Send(ctx context.Context, audio []byte) error
	// CloseSend signals end-of-audio; the provider flushes remaining
	// events and Events ends. Idempotent.
	CloseSend(ctx context.Context) error
	// Events yields transcript events until the stream ends. Single use.
	// After it ends, Err reports the terminal error, nil on clean end.
	Events() iter.Seq[TranscriptEvent]
	Err() error
	Close() error // aborts without flushing; idempotent
}

type StreamingTranscriptionModel interface {
	StreamTranscribe(ctx context.Context, call StreamTranscriptionCall) (TranscriptionStream, error)
	ModelID() string
	ProviderName() string
}
```

`ai/stream_transcribe.go` — validation + passthrough (NO retry — live connection):
```go
type StreamTranscribeOpts struct {
	Model           provider.StreamingTranscriptionModel
	MediaType       string
	Language        string
	SampleRate      int
	ProviderOptions map[string]any
}
func StreamTranscribe(ctx context.Context, opts StreamTranscribeOpts) (provider.TranscriptionStream, error)
```

**Deepgram live** (`providers/deepgram/live.go`): `(p *Provider) StreamingTranscriptionModel(id string) provider.StreamingTranscriptionModel`. Dial `wss://<host>/v1/listen?model=<id>&language=..&sample_rate=..&encoding=..` (derive host by swapping the scheme on baseURL so WithBaseURL fixture servers work — `http://x` → `ws://x`; encoding/sample_rate from MediaType+SampleRate, e.g. `audio/pcm;rate=16000` → `encoding=linear16&sample_rate=16000`, pass through unmapped media types by omitting encoding params; ProviderOptions as extra query params like the REST path). Header `Authorization: Token <key>`. Send: binary frames of raw audio. CloseSend: text frame `{"type":"CloseStream"}`. Events: parse JSON messages of type `Results`: `channel.alternatives[0].transcript` (skip empty), `is_final`, `start`, `start+duration` → EndSec. Server close / `Metadata` end → clean end.
**OpenAI realtime transcription** (`providers/openai/realtime_transcription.go`): `(p *Provider) StreamingTranscriptionModel(id string) ...` (id = transcription model, e.g. "gpt-4o-transcribe"). Dial `wss://<host>/v1/realtime?intent=transcription` (scheme-swap baseURL), headers `Authorization: Bearer <key>` + `OpenAI-Beta: realtime=v1`. On open, send `transcription_session.update` with `input_audio_format` (from MediaType: "audio/pcm"→"pcm16"; default pcm16) and `input_audio_transcription: {"model": id, "language": Language(omit "")}` + ProviderOptions["openai"] merged into the session object. Send: `{"type":"input_audio_buffer.append","audio":"<base64>"}`. CloseSend: `{"type":"input_audio_buffer.commit"}`. Events: `conversation.item.input_audio_transcription.delta` → Final:false; `...completed` → Final:true with full transcript; `error` events → Err. Times unknown → zeros.

**Tests:** ai: validation + passthrough + mock. Providers: in-test websocket SERVER (reusing the test-side frame codec from Task 2 — export a tiny `websockettest` helper package under internal/websocket/websockettest with Accept(listener)/ReadFrame/WriteFrame test utilities, built in Task 2's test code but promoted here if needed) scripting: handshake auth header assertion, session-config first message (openai), binary audio passthrough (deepgram), event sequences (interim→final), CloseSend wire shape, server close → clean end vs error event → Err, ctx cancel mid-stream, Close idempotency. -race on both provider packages and ai.

- [ ] **Step 1: provider interface + ai.StreamTranscribe + mock → green. Commit** — `feat: streaming transcription surface`
- [ ] **Step 2: Deepgram live + OpenAI realtime transcription w/ fixture WS servers → green. Full check suite. Commit** — `feat: Deepgram live + OpenAI realtime streaming transcription`

---

### Task 4: ai.Translate (OpenAI audio translation) + minimal OpenAI realtime voice session

**Files:**
- Create: `provider/translation.go`, `ai/translate.go`, `ai/translate_test.go`, `internal/openaicompat/translation.go` (+test), `providers/openai/realtime.go`, `providers/openai/realtime_test.go`
- Modify: `providers/openai/openai.go` (+TranslationModel method), `ai/registry.go` (NO new lookup — translation and realtime are niche; skip registry this wave, note in docs), `ai/aitest/mock.go` (+MockTranslationModel)

**Interfaces (Produces):**

`provider/translation.go` (mirrors transcription.go):
```go
type TranslationCall struct {
	Audio           []byte
	MediaType       string
	Prompt          string
	ProviderOptions map[string]any
}
type TranslationResponse struct {
	Text        string // English translation
	Language    string // detected source language when reported
	DurationSec float64
	Raw         json.RawMessage
}
type TranslationModel interface {
	Translate(ctx context.Context, call TranslationCall) (*TranslationResponse, error)
	ModelID() string
	ProviderName() string
}
```
`ai/translate.go`: `TranslateOpts{Model, Audio, MediaType, Prompt, MaxRetries, ProviderOptions}` → `TranslateResult{Text, Language string, DurationSec float64}` — same retry skeleton as ai.Transcribe (guards ErrModelRequired/ErrAudioRequired).
`internal/openaicompat/translation.go`: `NewTranslationModel(cfg Config, id string)` — multipart POST `{base}/audio/translations` (file part named per ExtForMediaType — reuse internal/transcribeutil.ExtForMediaType; fields model, prompt(omit ""), response_format=verbose_json) → `{"text","language","duration"}`. ProviderOptions["openai"] (cfg.Name) merged as extra multipart fields (stringified scalars). `providers/openai`: `(p *Provider) TranslationModel(id string) provider.TranslationModel`.

**Realtime voice session** (`providers/openai/realtime.go`) — OpenAI-specific, no generic provider interface this wave (documented):
```go
type RealtimeConfig struct {
	Model             string // e.g. "gpt-4o-realtime-preview"
	Voice             string
	Instructions      string
	InputAudioFormat  string // "pcm16" default
	OutputAudioFormat string // "pcm16" default
	ProviderOptions   map[string]any // merged into session.update's session object, wins per key
}

func (p *Provider) RealtimeSession(ctx context.Context, cfg RealtimeConfig) (*RealtimeSession, error)

type RealtimeSession struct{ /* wraps internal/websocket.Conn */ }

func (s *RealtimeSession) SendAudio(ctx context.Context, audio []byte) error   // input_audio_buffer.append (base64)
func (s *RealtimeSession) CommitAudio(ctx context.Context) error               // input_audio_buffer.commit
func (s *RealtimeSession) SendText(ctx context.Context, text string) error     // conversation.item.create (user message)
func (s *RealtimeSession) CreateResponse(ctx context.Context) error            // response.create
func (s *RealtimeSession) Events() iter.Seq[RealtimeEvent]                     // single use
func (s *RealtimeSession) Err() error
func (s *RealtimeSession) Close() error                                        // idempotent

type RealtimeEvent struct {
	Type       string          // the raw server event type
	AudioDelta []byte          // decoded, for response.output_audio.delta / response.audio.delta
	TextDelta  string          // response.output_text.delta / response.text.delta / audio transcript deltas
	Raw        json.RawMessage // always the full event
}
```
Dial `wss://<host>/v1/realtime?model=<cfg.Model>` (scheme-swap baseURL), headers Bearer + `OpenAI-Beta: realtime=v1`; on open send `session.update` from cfg (omit empties); every server event surfaces as RealtimeEvent with Raw always set and AudioDelta/TextDelta populated for the known delta types (accept BOTH the old `response.audio.delta`/`response.audio_transcript.delta` and new `response.output_audio.delta`/`response.output_text.delta` names); `error` type events → recorded, iteration continues (session-level errors only end iteration when the socket dies). Server close → clean end.

**Tests:** translation: multipart shape (file/model/prompt fields, options merge), verbose_json parse, 401/429, ctx. realtime: fixture WS server asserting session.update first message w/ config mapping + options merge; SendAudio base64 wire shape; SendText item shape; event surfacing (audio delta decode, text delta, unknown type → Raw-only); error event recorded; server close → clean end; Close idempotent; ctx cancel. -race.

- [ ] **Step 1: Translate surface + openaicompat + tests → green. Commit** — `feat: audio translation — provider.TranslationModel, ai.Translate, OpenAI`
- [ ] **Step 2: RealtimeSession + fixture WS tests → green. Full check suite. Commit** — `feat: minimal OpenAI realtime voice session`

---

### Task 5: Files & skills — FilePart references, provider.FileStore, OpenAI/Anthropic files, Anthropic skills

**Files:**
- Create: `provider/files.go`, `ai/upload_file.go`, `ai/upload_file_test.go`, `providers/openai/files.go` (+test), `providers/anthropic/files.go` (+test), `providers/anthropic/skills.go` (+test)
- Modify: `provider/message.go` (FilePart +2 fields + doc), `internal/openaicompat/wire.go`, `providers/anthropic/wire.go`, `internal/geminicompat/wire.go` (converter branches), `ai/aitest/mock.go` (+MockFileStore)

**Interfaces (Produces):**

`provider/message.go` — FilePart gains (ADDITIVE):
```go
	// FileID references a previously-uploaded provider file (see
	// provider.FileStore). URL references an externally-hosted file.
	// Exactly one of Data/FileID/URL should be set. Support:
	// FileID — openaicompat ("file" part w/ file_id), anthropic ("document"
	// w/ source {"type":"file","file_id"}). URL — geminicompat (fileData
	// fileUri; also accepts Gemini Files API URIs), anthropic PDFs
	// ("document" w/ source {"type":"url"}). Families without support for
	// the set variant reject the message (same rule as unsupported Data
	// media types).
	FileID string
	URL    string
```
Converter branches (each family's existing FilePart case extended; Data path unchanged): openaicompat FileID → `{"type":"file","file":{"file_id":...}}` (URL unsupported → error); anthropic FileID → document source `{"type":"file","file_id":...}`, URL → document source `{"type":"url","url":...}`; geminicompat URL → `{"fileData":{"fileUri":..., "mimeType": MediaType(omit "")}}` (FileID unsupported → error); bedrock: both unsupported → error (comment).

`provider/files.go`:
```go
type FileUploadCall struct {
	Data            []byte
	Filename        string
	MediaType       string
	Purpose         string // provider-specific, e.g. openai "user_data"; anthropic ignores
	ProviderOptions map[string]any
}
type FileInfo struct {
	ID        string
	Filename  string
	SizeBytes int64
	MediaType string
	Raw       json.RawMessage
}
type FileStore interface {
	UploadFile(ctx context.Context, call FileUploadCall) (*FileInfo, error)
	DeleteFile(ctx context.Context, id string) error
	ProviderName() string
}
```
`ai/upload_file.go`: `UploadFileOpts{Store provider.FileStore, Data, Filename, MediaType, Purpose, MaxRetries, ProviderOptions}` → `*provider.FileInfo` with the standard retry skeleton (guards ErrStoreRequired [new sentinel], ErrDataRequired [new or reuse], Filename required). `DeleteFile(ctx, DeleteFileOpts{Store, ID, MaxRetries})`.

**OpenAI files** (`providers/openai/files.go`): `(p *Provider) Files() provider.FileStore` — multipart POST `{base}/files` (fields file, purpose default "user_data") → `{"id","filename","bytes"}`; DELETE `{base}/files/{id}`. **Anthropic files** (`providers/anthropic/files.go`): `(p *Provider) Files() provider.FileStore` — multipart POST `{base}/v1/files` with headers x-api-key + anthropic-version + `anthropic-beta: files-api-2025-04-14` → `{"id","filename","size_bytes","mime_type"}`; DELETE `{base}/v1/files/{id}` (same beta header). Beta header at the files call sites only, never on the shared language-model path.

**Anthropic skills** (`providers/anthropic/skills.go`) — provider-specific, no generic interface (documented):
```go
type SkillInfo struct { ID, DisplayName, Version string; Raw json.RawMessage }
func (p *Provider) UploadSkill(ctx context.Context, call UploadSkillCall) (*SkillInfo, error)
type UploadSkillCall struct { Zip []byte; DisplayName string; ProviderOptions map[string]any }
func (p *Provider) DeleteSkill(ctx context.Context, id string) error
```
Multipart POST `{base}/v1/skills` (file part named "files[]" as skill.zip, display_name field) with `anthropic-beta: skills-2025-10-02`; DELETE `{base}/v1/skills/{id}`. All response fields into Raw; ID/DisplayName/Version parsed.

**Tests:** wire converters per family per new variant incl. rejection errors; round-trip through GenerateText transcripts (FilePart FileID in a user message reaches the wire); files clients: multipart shapes, beta headers asserted (anthropic), delete paths, 401/429, ctx; skills: multipart + beta header + parse; ai.UploadFile/DeleteFile validation + retry; MockFileStore.

- [ ] **Step 1: FilePart fields + converters + tests → green. Commit** — `feat: file references in prompts (FileID/URL on FilePart)`
- [ ] **Step 2: FileStore + openai/anthropic files + ai.UploadFile/DeleteFile + anthropic skills → green. Full check suite. Commit** — `feat: file upload (OpenAI/Anthropic) and Anthropic skills`

---

### Task 6: Wave-12 docs + CHANGELOG

**Files:**
- Modify: `docs/core/media.md` (video section w/ matrix; translation section; streaming transcription section; realtime session section), `docs/getting-started.md` (no new env vars — verify), `docs/providers/{luma,fal,replicate}.md` (video), `docs/providers/deepgram.md` (live), `docs/providers/openai.md` (realtime transcription, realtime session, translation, files), `docs/providers/anthropic.md` (files, skills, beta headers), `docs/providers/README.md` (+video/streaming-STT/translation matrix columns or rows), `docs/core/tools.md` or `docs/core/generating-text.md` (FilePart reference variants — wherever FilePart is documented today: grep), `README.md` (features), `docs/migrating-from-vercel-ai-sdk.md` (video/streaming STT/realtime/files/skills → Shipped; StreamTranslate → shipped-as-ai.Translate REST with the ruling; WebRTC row unchanged), `CHANGELOG.md` (Wave 12 entries), `docs/README.md` (verify — likely no new pages; media.md covers all), `docs/architecture.md` (internal/websocket one-liner if the page lists internals — verify).
- Verification discipline as prior waves (compile-verified snippets, grep-true claims, matrices, links). Live-testing notes on every touched provider page for the new endpoints (esp. realtime/live WS — impossible to fixture-verify against real servers).

- [ ] **Step 1: Write/update all; verify. Full check suite. Commit** — `docs: wave 12 — video, streaming transcription, realtime, translation, files & skills`

---

## Self-Review Notes

- Scope rulings this wave: StreamTranslate → ai.Translate (REST) with migration-doc note; uploadSkill → Anthropic-only client; realtime session OpenAI-only with no generic provider interface; no Registry lookups for translation/streaming-transcription/realtime/filestore (Registry stays generation-model-focused; documented).
- Task ordering: 1 (independent) → 2 (websocket) → 3,4 (depend on 2; 3 also promotes the test-server helpers into internal/websocket/websockettest for reuse — Task 2 should ALREADY place its test frame codec in internal/websocket/websockettest to avoid churn: amend Task 2 to create `internal/websocket/websockettest/websockettest.go` (exported test helpers: Accept, ReadMessage, WriteMessage, WriteClose) used by its own tests) → 5 (independent of 2) → 6.
- The scheme-swap rule (`http(s)://` → `ws(s)://` on configured baseURLs) is what makes WS fixture tests work with httptest-style listeners — both WS providers must derive their dial URL from baseURL, never hardcode.
- FilePart doc comment in message.go must be updated wholesale (it enumerates per-family support — the new variants extend that enumeration).
- Realtime event-name duality (response.audio.delta vs response.output_audio.delta) is deliberate future-proofing — both mapped, tested.
