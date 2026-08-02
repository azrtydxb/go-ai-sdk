# go-ai-sdk Wave 4 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the SDK beyond language models: image generation (`ai.GenerateImage`), speech synthesis (`ai.GenerateSpeech`), and transcription (`ai.Transcribe`), with provider implementations for OpenAI (all three), Google (Imagen), xAI (images), Groq (transcription), and a new ElevenLabs provider (speech + transcription).

**Architecture:** Three new model interfaces in `provider` (ImageModel, SpeechModel, TranscriptionModel) mirroring the LanguageModel/EmbeddingModel pattern; three thin orchestration functions in `ai` (validation + retries, like Embed); provider implementations reuse each provider's existing transport/auth plumbing. OpenAI-compatible image/transcription endpoints are implemented once in `internal/openaicompat` and exposed by the openai/xai/groq presets.

**Tech Stack:** Go 1.26, stdlib only (mime/multipart for uploads).

## Global Constraints

- Module `github.com/azrtydxb/go-ai-sdk`; stdlib only, zero external dependencies.
- Providers NEVER retry — retries live in `ai` (same retry.Do + RetryError pattern as GenerateText/Embed, default 2).
- Existing tests stay green after every task; `go vet ./... && go build ./... && go test ./... && gofmt -l .` clean before every commit.
- Non-2xx → `ai.NewAPICallError` everywhere; ctx cancellation passthrough everywhere.
- Commit messages conventional, each ending with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`

---

### Task 1: `provider` capability interfaces + `ai` core functions + mocks

**Files:**
- Create: `provider/image.go`, `provider/speech.go`, `provider/transcription.go`
- Create: `ai/generate_image.go`, `ai/generate_speech.go`, `ai/transcribe.go`
- Modify: `ai/aitest/mock.go` (MockImageModel, MockSpeechModel, MockTranscriptionModel)
- Test: `ai/generate_image_test.go`, `ai/generate_speech_test.go`, `ai/transcribe_test.go`

**Interfaces:**
- Produces (used verbatim by Tasks 2–4):

```go
// provider/image.go
type GeneratedImage struct {
    Data      []byte // raw image bytes
    MediaType string // e.g. "image/png"
}
type ImageCall struct {
    Prompt          string
    N               int    // number of images; 0 → provider default (1)
    Size            string // e.g. "1024x1024"; "" → provider default
    AspectRatio     string // e.g. "16:9"; providers that use size ignore this and vice versa
    Seed            *int64
    ProviderOptions map[string]any
}
type ImageResponse struct {
    Images []GeneratedImage
    Raw    json.RawMessage
}
type ImageModel interface {
    GenerateImages(ctx context.Context, call ImageCall) (*ImageResponse, error)
    ModelID() string
    ProviderName() string
}

// provider/speech.go
type SpeechCall struct {
    Text            string
    Voice           string   // provider-specific voice id; "" → provider default
    OutputFormat    string   // e.g. "mp3", "wav"; "" → provider default
    Speed           *float64
    Language        string   // BCP-47 hint where supported
    ProviderOptions map[string]any
}
type SpeechResponse struct {
    Audio     []byte
    MediaType string // e.g. "audio/mpeg"
}
type SpeechModel interface {
    GenerateSpeech(ctx context.Context, call SpeechCall) (*SpeechResponse, error)
    ModelID() string
    ProviderName() string
}

// provider/transcription.go
type TranscriptionCall struct {
    Audio           []byte
    MediaType       string // e.g. "audio/mpeg"; used for the upload filename/content-type
    Language        string // optional hint
    Prompt          string // optional context prompt
    ProviderOptions map[string]any
}
type TranscriptSegment struct {
    Text     string
    StartSec float64
    EndSec   float64
}
type TranscriptionResponse struct {
    Text        string
    Segments    []TranscriptSegment // empty when the provider doesn't return segments
    Language    string              // detected language, "" if not reported
    DurationSec float64             // 0 if not reported
    Raw         json.RawMessage
}
type TranscriptionModel interface {
    Transcribe(ctx context.Context, call TranscriptionCall) (*TranscriptionResponse, error)
    ModelID() string
    ProviderName() string
}
```

```go
// ai/generate_image.go
type GenerateImageOpts struct {
    Model           provider.ImageModel // required
    Prompt          string              // required
    N               int
    Size            string
    AspectRatio     string
    Seed            *int64
    MaxRetries      *int
    ProviderOptions map[string]any
}
type GenerateImageResult struct {
    Image  provider.GeneratedImage   // first image
    Images []provider.GeneratedImage
}
func GenerateImage(ctx context.Context, opts GenerateImageOpts) (*GenerateImageResult, error)
// validation: Model non-nil ("ai: model is required"), Prompt non-empty
// ("ai: prompt is required"); wraps the model call in retry.Do exactly like
// Embed; empty Images in a success response → error "ai: image model
// returned no images".

// ai/generate_speech.go
type GenerateSpeechOpts struct {
    Model           provider.SpeechModel
    Text            string
    Voice           string
    OutputFormat    string
    Speed           *float64
    Language        string
    MaxRetries      *int
    ProviderOptions map[string]any
}
type GenerateSpeechResult struct {
    Audio     []byte
    MediaType string
}
func GenerateSpeech(ctx context.Context, opts GenerateSpeechOpts) (*GenerateSpeechResult, error)
// validation: Model required; Text non-empty ("ai: text is required");
// empty Audio on success → "ai: speech model returned no audio".

// ai/transcribe.go
type TranscribeOpts struct {
    Model           provider.TranscriptionModel
    Audio           []byte
    MediaType       string
    Language        string
    Prompt          string
    MaxRetries      *int
    ProviderOptions map[string]any
}
type TranscribeResult struct {
    Text        string
    Segments    []provider.TranscriptSegment
    Language    string
    DurationSec float64
}
func Transcribe(ctx context.Context, opts TranscribeOpts) (*TranscribeResult, error)
// validation: Model required; Audio non-empty ("ai: audio is required").
```

aitest mocks follow MockEmbedder's shape: scripted response + Err field + recorded calls (e.g. `MockImageModel{Response *provider.ImageResponse; Err error; Calls []provider.ImageCall}`), ProviderName "aitest", ModelID "mock".

Tests (per function): happy path maps opts→call and result correctly; validation errors; retry-then-RetryError on 500 APICallError (BaseDelay=1ms via existing TestMain); empty-result error.

- [ ] **Step 1: Failing tests → Step 2: implement → Step 3: pass. Step 4: full check suite. Commit** — `feat: image, speech, and transcription model capabilities`

---

### Task 2: openaicompat image + transcription; openai speech; expose on openai preset

**Files:**
- Create: `internal/openaicompat/image.go`, `internal/openaicompat/transcription.go`, `providers/openai/speech.go` (speech is OpenAI-only for now — implement directly in the openai package using cfg-style plumbing via openaicompat? DECISION: put speech in `internal/openaicompat/speech.go` too — xAI/groq don't expose it, but the code is identical shape and keeps transport in one place)
- Modify: `providers/openai/openai.go` (ImageModel/SpeechModel/TranscriptionModel constructors), `internal/openaicompat/compattest/compattest.go` (fixture endpoints)
- Test: `providers/openai/image_test.go`, `providers/openai/speech_test.go`, `providers/openai/transcription_test.go`, openaicompat request-shape tests

**Interfaces:**
- Produces:

```go
// internal/openaicompat
func NewImageModel(cfg Config, modelID string) provider.ImageModel
func NewSpeechModel(cfg Config, modelID string) provider.SpeechModel
func NewTranscriptionModel(cfg Config, modelID string) provider.TranscriptionModel

// providers/openai
func (p *Provider) ImageModel(id string) provider.ImageModel               // e.g. "gpt-image-1", "dall-e-3"
func (p *Provider) SpeechModel(id string) provider.SpeechModel             // e.g. "gpt-4o-mini-tts", "tts-1"
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel // e.g. "gpt-4o-transcribe", "whisper-1"
```

Wire mappings:
- **Images**: `POST {base}/images/generations` `{"model","prompt","n","size","response_format":"b64_json"}` (omit n/size when zero-valued; seed unsupported → ignored with comment; AspectRatio unsupported → error if set: `"<name>: aspect ratio is not supported; use Size"`). Response `{"data":[{"b64_json":...}]}` → decode base64 → GeneratedImage{Data, MediaType:"image/png"}. gpt-image-1 ignores response_format and always returns b64_json — sending it is still accepted for dall-e models; keep sending it.
- **Speech**: `POST {base}/audio/speech` `{"model","input","voice","response_format","speed"}` (voice default "alloy" when empty — OpenAI requires voice; format default "mp3"). Response = raw audio bytes; MediaType from format: mp3→audio/mpeg, wav→audio/wav, opus→audio/opus, aac→audio/aac, flac→audio/flac, pcm→audio/pcm.
- **Transcription**: `POST {base}/audio/transcriptions` multipart/form-data: `file` (filename "audio.<ext from MediaType: audio/mpeg→mp3, audio/wav→wav, audio/mp4→mp4, audio/webm→webm, else bin>", content-type = MediaType), `model`, optional `language`, `prompt`, and `response_format=verbose_json`. Response `{"text","language","duration","segments":[{"text","start","end"}]}` → mapped. Models that reject verbose_json (gpt-4o-transcribe) → the request still sends `verbose_json`? NO — DECISION: send `response_format=json` for model IDs containing "gpt-4o", else `verbose_json` (whisper); parse both shapes (json shape has only text).
- All three: auth via `cfg.setAuthHeader` (works for azure's api-key too), empty-BaseURL check, non-2xx → NewAPICallError.
- compattest: add fixture endpoints `/images/generations`, `/audio/speech`, `/audio/transcriptions` with canned responses (image: one 1x1 PNG base64; speech: bytes "FAKEAUDIO"; transcription: verbose_json with 2 segments), still recording Requests()/headers. Request-shape tests assert the wire bodies (multipart parsed with mime/multipart on the recorded raw body).

- [ ] **Step 1: Failing tests → implement → pass. Step 2: full check suite. Commit** — `feat: OpenAI image, speech, and transcription models`

---

### Task 3: Google Imagen images + xAI images + Groq transcription

**Files:**
- Create: `providers/google/image.go` (Imagen via :predict), `providers/vertex/image.go` (same wire at vertex paths)
- Modify: `providers/xai/xai.go` (+ImageModel via openaicompat), `providers/groq/groq.go` (+TranscriptionModel via openaicompat)
- Test: `providers/google/image_test.go`, `providers/vertex/image_test.go`, xai/groq test additions

**Interfaces:**
- Produces: `google.Provider.ImageModel(id)` and `vertex.Provider.ImageModel(id)` (e.g. "imagen-3.0-generate-002"); `xai.Provider.ImageModel(id)` (e.g. "grok-2-image"); `groq.Provider.TranscriptionModel(id)` (e.g. "whisper-large-v3").

Wire mappings:
- **Imagen (Gemini API)**: `POST {base}/models/{id}:predict` (google auth header) `{"instances":[{"prompt":...}],"parameters":{"sampleCount":N,"aspectRatio":"16:9"(when set),"seed":seed(when set)}}` → `{"predictions":[{"bytesBase64Encoded":...,"mimeType":"image/png"}]}`. Size unsupported → error if set ("google: size is not supported; use AspectRatio"). Vertex: same body at the vertex path (`…/publishers/google/models/{id}:predict`, Bearer auth) — implement once in `internal/geminicompat/image.go` (`NewImageModel(cfg Config, modelID string)`) using Config.EndpointFor with method "predict" and reuse from both providers.
- **xAI images**: openaicompat `NewImageModel` with xai config — but xAI's API ignores `size` and doesn't accept `response_format`... DECISION: xAI accepts the OpenAI images shape with `response_format:"b64_json"`; reuse openaicompat.NewImageModel unchanged. Test via compattest images endpoint (ProviderName "xai").
- **Groq transcription**: openaicompat `NewTranscriptionModel` with groq config; groq is whisper-only → always `verbose_json` (the "gpt-4o" carve-out doesn't trigger). Test via compattest.

- [ ] **Step 1: geminicompat image (failing test w/ fixture at google paths) → implement → both google+vertex wired. Step 2: xai/groq one-liners + tests. Step 3: full check suite. Commit** — `feat: Imagen, xAI image, and Groq transcription models`

---

### Task 4: ElevenLabs provider (speech + transcription)

**Files:**
- Create: `providers/elevenlabs/{elevenlabs.go,speech.go,transcription.go}`
- Test: `providers/elevenlabs/{elevenlabs_test.go,speech_test.go,transcription_test.go}`

**Interfaces:**
- Produces:

```go
// ElevenLabs: speech synthesis and transcription. No language models.
func New(opts ...Option) *Provider // WithAPIKey (env ELEVENLABS_API_KEY), WithBaseURL (default https://api.elevenlabs.io), WithHTTPClient
func (p *Provider) SpeechModel(id string) provider.SpeechModel             // e.g. "eleven_multilingual_v2"
func (p *Provider) TranscriptionModel(id string) provider.TranscriptionModel // e.g. "scribe_v1"
```

Wire mappings:
- **Speech**: `POST {base}/v1/text-to-speech/{voiceID}?output_format={fmt}` — header `xi-api-key`. VoiceID from SpeechCall.Voice; empty → default voice id `"21m00Tcm4TlvDq8ikWAM"` (Rachel, ElevenLabs' documented default) with a doc comment. Body `{"text","model_id":<modelID>,"language_code":<Language when set>}`. output_format from OutputFormat: "mp3"/""→`mp3_44100_128`→audio/mpeg; "pcm"→`pcm_44100`→audio/pcm; "ulaw"→`ulaw_8000`→audio/basic; anything else passed through verbatim with MediaType "application/octet-stream". Response = audio bytes.
- **Transcription**: `POST {base}/v1/speech-to-text` multipart: `file` (audio bytes, content type MediaType), `model_id`, optional `language_code`. Response `{"text","language_code","words":[{"text","start","end","type"}]}` → Text, Language; words with type "word" → segments (per-word segments: Text/StartSec/EndSec). Duration = last word's end.
- Errors: non-2xx → NewAPICallError (message from `{"detail":{"message":...}}` or `{"detail":"..."}` best-effort).
- Tests: fixture server asserting xi-api-key header, path (voice id + output_format query), body shapes; speech happy path + default voice + format mapping; transcription multipart parse + word→segment mapping; 401 → APICallError.

- [ ] **Step 1: Failing tests → implement → pass. Step 2: full check suite. Commit** — `feat: ElevenLabs provider (speech + transcription)`

---

### Task 5: Examples + docs + spec addendum

**Files:**
- Create: `examples/generate-image/main.go`, `examples/generate-speech/main.go`, `examples/transcribe/main.go`
- Modify: `README.md`, `docs/superpowers/specs/2026-08-02-go-ai-sdk-design.md`

**Work:**
- Examples (~30 lines, env-guarded like existing ones): generate-image (openai gpt-image-1, writes out.png), generate-speech (openai tts writes out.mp3), transcribe (openai whisper on a file path arg).
- README: new "Beyond text" section with the three functions and a capability matrix for the new model types (image: openai, google, vertex, xai; speech: openai, elevenlabs; transcription: openai, groq, elevenlabs). Verify each against code; check table cell counts.
- Spec: append a "## Capability extension (wave 4, shipped)" section documenting the three interfaces and the provider matrix (this fulfills the original spec's "separate spec" note for these capabilities).

- [ ] **Step 1: Examples compile (`go build ./...`). Step 2: docs verified. Step 3: full check suite. Commit** — `docs: image/speech/transcription capabilities`

---

## Self-Review Notes

- **Type consistency:** `provider.{ImageModel,SpeechModel,TranscriptionModel}` + call/response types produced in T1 and consumed verbatim in T2–T4; `openaicompat.New{Image,Speech,Transcription}Model` produced T2, reused T3 (xai, groq); `geminicompat.NewImageModel` produced and consumed in T3.
- **Vercel parity mapping:** generateImage → ai.GenerateImage; generateSpeech (experimental) → ai.GenerateSpeech; transcribe (experimental) → ai.Transcribe. Provider coverage prioritizes the highest-usage integrations; remaining Vercel image/speech providers (fal, replicate, luma, deepgram, lmnt, hume) are documented as not-yet-included in the README matrix — the interfaces make them straightforward follow-ups.
- **No placeholders**; endpoints, defaults, and format mappings pinned above.
