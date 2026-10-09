# OpenAI-compatible (self-hosted)

`providers/openaicompatible` talks to any server that speaks the OpenAI HTTP
API: vLLM, Ollama, llama.cpp, Hugging Face TEI, speaches, LiteLLM and similar.
It is a thin public wrapper over the shared `internal/openaicompat` base.
Design record: `.procoder/adr/0001-public-openaicompatible-provider.md`.

Unlike the vendor presets there is **no default base URL** and the API key is
**optional**: with no key, no `Authorization` header is sent. A missing base
URL is reported as an error when a model is called, never as a request to the
public internet.

```go
import "github.com/azrtydxb/go-ai-sdk/providers/openaicompatible"

p := openaicompatible.New("http://vllm.internal:8000/v1",
	openaicompatible.WithAPIKey("..."),                      // optional
	openaicompatible.WithHeader("X-Tenant", "acme"),         // static headers
	openaicompatible.WithName("vllm"),                       // ProviderName() / ProviderOptions key
)

chat := p.Chat("Qwen/Qwen3-32B")                  // ai.GenerateText / ai.StreamText
embed := p.Embedding("BAAI/bge-m3")               // ai.Embed / ai.EmbedMany
tts := p.Speech("MediaTek-Research/BreezyVoice")  // ai.GenerateSpeech
stt := p.Transcription("MediaTek-Research/Breeze-ASR-25") // ai.Transcribe
rr := p.Rerank("BAAI/bge-reranker-v2-m3")         // ai.Rerank
```

Other options: `WithHTTPClient`, `WithHeaders`, `WithEmbeddingBatchSize`
(default 32, TEI's default client batch limit), `WithTranscriptionFormat`,
`WithMaxTokensParam`.

## Capabilities

- **Chat** — streaming through `iter.Seq`; the stream requests
  `stream_options.include_usage`, so token usage arrives in the final
  `FinishPart`. Tool calling and native JSON mode follow the shared base.
- **Embeddings** — `/embeddings`, with usage.
- **Speech** — `/audio/speech`. Model and voice strings pass through
  verbatim, so server-defined voices work. The returned media type follows the
  server's `audio/*` Content-Type when present.
- **Transcription** — `/audio/transcriptions` as multipart, with `language`
  (for example `zh`) and `prompt`. The default `response_format` is
  `verbose_json`, which yields segments; use `WithTranscriptionFormat("json")`
  for servers without it (older vLLM Whisper builds) or `"text"` for plain
  text.
- **Rerank** — two selectable wire shapes, both through `ai.Rerank`, results
  sorted by score descending and `TopN` enforced client-side.

## Reranking

```go
// vLLM / OpenAI-compatible: POST {base}/rerank
// {model, query, documents, top_n} -> {results:[{index, relevance_score}]}
vllm := openaicompatible.New("http://vllm:8000/v1").Rerank("BAAI/bge-reranker-v2-m3")

// Hugging Face TEI: POST {base}/rerank
// {query, texts} -> [{index, score}]
tei := openaicompatible.New("http://tei:8080").
	Rerank("bge-reranker", openaicompatible.WithRerankShape(openaicompatible.RerankTEI))
```

TEI serves `/rerank` at its root, so its base URL has no `/v1`; the model is
fixed by the server and the id is informational.

## Breeze (MediaTek) and other self-hosted voice

MediaTek Breeze-ASR-25 (Whisper-based speech recognition, Traditional Chinese
and Taiwanese Mandarin/English code-switching) and BreezyVoice (speech
synthesis) have no dedicated package: serve them behind an OpenAI-compatible
audio API (vLLM for Whisper-family models, a speaches-style server) and point
this provider at it.

```go
p := openaicompatible.New("http://gpu-box:8000/v1")

tr, err := ai.Transcribe(ctx, ai.TranscribeOpts{
	Model:     p.Transcription("MediaTek-Research/Breeze-ASR-25"),
	Audio:     wav,
	MediaType: "audio/wav",
	Language:  "zh",
})

sp, err := ai.GenerateSpeech(ctx, ai.GenerateSpeechOpts{
	Model:        p.Speech("MediaTek-Research/BreezyVoice"),
	Text:         "你好，歡迎使用語音合成。",
	Voice:        "my-server-voice",
	OutputFormat: "wav",
})
```

See `examples/self-hosted-voice`. Notes: `ai.GenerateSpeechOpts.Language` is
not sent on the wire (the OpenAI speech API has none); pass server-specific
fields through `ProviderOptions["openaicompatible"]` (or your `WithName`).

## Live testing

`providers/openaicompatible/live_test.go` runs the provider through the `ai.*`
entry points against real servers. Each test skips unless its variables are
set, so plain `go test ./...` stays hermetic.

| Test                   | Variables                                                                                                   |
| ---------------------- | ----------------------------------------------------------------------------------------------------------- |
| `TestLiveRerankTEI`    | `OPENAICOMPAT_LIVE_TEI_URL` (TEI root, no `/v1`)                                                            |
| `TestLiveRerankOpenAI` | `OPENAICOMPAT_LIVE_RERANK_URL`, `_RERANK_MODEL`                                                             |
| `TestLiveEmbed`        | `OPENAICOMPAT_LIVE_EMBED_URL`, `_EMBED_MODEL`                                                               |
| `TestLiveChatStream`   | `OPENAICOMPAT_LIVE_CHAT_URL`, `_CHAT_MODEL`                                                                 |
| `TestLiveTranscribe`   | `OPENAICOMPAT_LIVE_STT_URL`, `_STT_MODEL`, `_STT_AUDIO` (file path), optional `_STT_LANG`, `_STT_MEDIATYPE` |
| `TestLiveSpeech`       | `OPENAICOMPAT_LIVE_TTS_URL`, `_TTS_MODEL`, `_TTS_VOICE`, optional `_TTS_OPTS` (`k=v,k=v` extra fields)      |

Verified on Apple Silicon (arm64 Docker, CPU only, 8 GB):

```sh
# TEI: reranker (RerankTEI) and a small embedder (/v1/embeddings)
docker run -d -p 18081:80 -v tei:/data ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-latest --model-id BAAI/bge-reranker-base
docker run -d -p 18082:80 -v tei:/data ghcr.io/huggingface/text-embeddings-inference:cpu-arm64-latest --model-id BAAI/bge-small-en-v1.5

# speaches: Breeze-ASR-25 (STT, verbose_json and text) and Kokoro (TTS)
docker run -d -p 18083:8000 -v hf:/home/ubuntu/.cache/huggingface/hub ghcr.io/speaches-ai/speaches:latest-cpu
curl -X POST localhost:18083/v1/models/phate334/Breeze-ASR-25-int8-CT2
curl -X POST localhost:18083/v1/models/speaches-ai/Kokoro-82M-v1.0-ONNX
say -v Meijia -o zh.aiff "你好，今天天氣很好，我想去公園散步。" && afconvert -f WAVE -d LEI16@16000 -c 1 zh.aiff zh.wav

# llama.cpp: chat (streaming + usage) and a reranker (RerankOpenAI shape)
docker run -d -p 18084:8080 -v llama:/root/.cache ghcr.io/ggml-org/llama.cpp:server -hf Qwen/Qwen2.5-0.5B-Instruct-GGUF:Q4_K_M --host 0.0.0.0
docker run -d -p 18085:8080 -v llama:/root/.cache ghcr.io/ggml-org/llama.cpp:server -hf gpustack/bge-reranker-v2-m3-GGUF:Q4_K_M --reranking --host 0.0.0.0

OPENAICOMPAT_LIVE_TEI_URL=http://localhost:18081 \
OPENAICOMPAT_LIVE_EMBED_URL=http://localhost:18082/v1 OPENAICOMPAT_LIVE_EMBED_MODEL=BAAI/bge-small-en-v1.5 \
OPENAICOMPAT_LIVE_STT_URL=http://localhost:18083/v1 OPENAICOMPAT_LIVE_STT_MODEL=phate334/Breeze-ASR-25-int8-CT2 \
OPENAICOMPAT_LIVE_STT_AUDIO=$PWD/zh.wav OPENAICOMPAT_LIVE_STT_LANG=zh \
OPENAICOMPAT_LIVE_TTS_URL=http://localhost:18083/v1 OPENAICOMPAT_LIVE_TTS_MODEL=speaches-ai/Kokoro-82M-v1.0-ONNX OPENAICOMPAT_LIVE_TTS_VOICE=af_heart \
OPENAICOMPAT_LIVE_RERANK_URL=http://localhost:18085 OPENAICOMPAT_LIVE_RERANK_MODEL=bge-reranker-v2-m3 \
OPENAICOMPAT_LIVE_CHAT_URL=http://localhost:18084/v1 OPENAICOMPAT_LIVE_CHAT_MODEL=qwen2.5-0.5b \
go test ./providers/openaicompatible -run Live -v
```

All of the above passed. The TEI `cpu-<version>` tags are amd64 only; use
`cpu-arm64-latest` on Apple Silicon. Breeze-ASR-25 returned correct Traditional
Chinese text for the Mandarin clip in both `verbose_json` and `text` formats
(CPU int8, roughly 25-35 s for a 4 s clip including model load).

### Server notes

- **vLLM** serves `/rerank`, `/v1/rerank` and `/v2/rerank`. Prefer a base URL
  without `/v1` for reranking (this provider appends `/rerank`); `/v1/rerank`
  works but logs a deprecation warning. Chat and embeddings still need `/v1`,
  so use two `New(...)` providers when mixing them.
- **llama.cpp** (`--reranking`) also exposes `/rerank` and `/v1/rerank` in the
  Jina/Cohere shape, which is the `RerankOpenAI` shape. Scores are raw logits.
- **TEI** serves `/rerank` at its root, so use the root URL only. Scores are
  sigmoid-normalised (0..1) unless `raw_scores` is true; pass it with
  `ProviderOptions: {"openaicompatible": {"raw_scores": true}}`. TEI does not
  report token usage for rerank. Its `/v1/embeddings` works with `Embedding`.
- **speaches** returns `Content-Type: audio/mp3` for mp3 (non-standard but
  `audio/*`, passed through). Its Kokoro build failed for Mandarin voices
  (`zf_*`: espeak backend rejects language `zh`; `language`/`lang_code` fields
  did not help), so TTS was verified with an English voice.
- **Kokoro-FastAPI** style servers select the language with `lang_code`; pass
  it via `ProviderOptions`.
- **BreezyVoice** is CUDA-only (nvidia/cuda amd64 image), so it was not run
  live. Its `api.py` serves one voice (the configured speaker prompt) per
  server instance, ignores `voice` and `response_format`, and always returns a
  22050 Hz WAV as `audio/wav`; the provider reports `audio/wav` regardless of
  the requested format (`TestSpeechBreezyVoiceContract`).
