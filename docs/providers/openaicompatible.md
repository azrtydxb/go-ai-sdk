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

⚠ **Live-testing note:** like every provider in this SDK, the contract is
verified against `httptest` servers, not against live vLLM, TEI or Breeze
deployments.
