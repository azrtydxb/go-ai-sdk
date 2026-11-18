// Package openaicompatible is the generic provider for self-hosted and
// third-party servers that speak the OpenAI HTTP API: vLLM, Ollama, TEI,
// llama.cpp, speaches, LiteLLM and similar.
//
// Unlike the vendor presets it has no default base URL (no request ever
// leaves for the public internet unless you say so) and its API key is
// optional: with no key, no Authorization header is sent. A missing base URL
// is reported as an error when a model is first called.
//
// It covers chat (streaming, with usage in the final part), embeddings,
// speech (/audio/speech), transcription (/audio/transcriptions) and
// reranking in the vLLM/OpenAI shape (/rerank) or the Hugging Face TEI
// shape (/rerank). Provider-level, it lists models (/models, including
// OpenRouter's modalities and supported_voices) and checks the API key
// (OpenRouter's /key, falling back to an authenticated /models).
package openaicompatible

import (
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/openaicompat"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

const (
	defaultName = "openaicompatible"

	// defaultEmbedBatch is a conservative batch size: TEI's default
	// max-client-batch-size is 32.
	defaultEmbedBatch = 32

	// defaultKeyInfoPath is OpenRouter's key-info endpoint; other servers
	// answer it with 404 and CheckKey falls back to /models.
	defaultKeyInfoPath = "/key"
)

// Provider builds models against one OpenAI-compatible endpoint.
type Provider struct {
	name         string
	baseURL      string
	apiKey       string
	headers      map[string]string
	httpClient   *http.Client
	embedBatch   int
	sttFormat    string
	maxTokensKey string
	nullContent  bool
	lenient      bool
	keyInfoPath  string
}

// Option configures a Provider.
type Option func(*Provider)

// WithAPIKey sets the bearer token. Optional: when empty, no Authorization
// header is sent.
func WithAPIKey(k string) Option { return func(p *Provider) { p.apiKey = k } }

// WithHeader adds one static header sent on every request. A key matching
// Authorization is ignored; use WithAPIKey.
func WithHeader(k, v string) Option {
	return func(p *Provider) {
		if p.headers == nil {
			p.headers = map[string]string{}
		}
		p.headers[k] = v
	}
}

// WithHeaders adds several static headers; see WithHeader.
func WithHeaders(h map[string]string) Option {
	return func(p *Provider) {
		for k, v := range h {
			WithHeader(k, v)(p)
		}
	}
}

// WithHTTPClient overrides the *http.Client used for requests.
func WithHTTPClient(c *http.Client) Option { return func(p *Provider) { p.httpClient = c } }

// WithName sets the name reported by ProviderName (default
// "openaicompatible"); it also keys ProviderOptions.
func WithName(n string) Option { return func(p *Provider) { p.name = n } }

// WithEmbeddingBatchSize sets EmbeddingModel.MaxBatchSize (default 32).
func WithEmbeddingBatchSize(n int) Option { return func(p *Provider) { p.embedBatch = n } }

// WithTranscriptionFormat sets the response_format sent to
// /audio/transcriptions. The default is "verbose_json" (text, language,
// duration, segments); servers without it, such as older vLLM Whisper
// builds, need "json". "text" is also understood.
func WithTranscriptionFormat(f string) Option { return func(p *Provider) { p.sttFormat = f } }

// WithMaxTokensParam sets the wire name for the output token limit (default
// "max_completion_tokens"; older servers want "max_tokens").
func WithMaxTokensParam(name string) Option { return func(p *Provider) { p.maxTokensKey = name } }

// WithNullAssistantContent sends "content":null on assistant messages that
// carry tool calls and no text, instead of omitting the key, for servers
// that require it.
func WithNullAssistantContent() Option { return func(p *Provider) { p.nullContent = true } }

// WithLenientStreaming makes chat streaming tolerant of sloppy servers and
// proxies: data lines that are not JSON are skipped, and a body that ends
// without finish_reason or [DONE] yields the accumulated ToolCallEnds and a
// FinishPart with FinishOther instead of an error. It is opt-in because a
// truncated stream then looks like a finished one.
func WithLenientStreaming() Option { return func(p *Provider) { p.lenient = true } }

// WithKeyInfoPath sets the key-info endpoint KeyChecker reads, relative to
// the base URL (default "/key", OpenRouter's). An empty path skips it, so
// CheckKey only validates the key with an authenticated GET /models.
func WithKeyInfoPath(path string) Option { return func(p *Provider) { p.keyInfoPath = path } }

// New creates a Provider for the endpoint at baseURL, the URL that
// precedes the OpenAI paths, normally ending in /v1 (for example
// "http://vllm:8000/v1"). baseURL is required; an empty value makes every
// model call return an error.
func New(baseURL string, opts ...Option) *Provider {
	p := &Provider{
		name:        defaultName,
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  http.DefaultClient,
		embedBatch:  defaultEmbedBatch,
		keyInfoPath: defaultKeyInfoPath,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Provider) config() openaicompat.Config {
	return openaicompat.Config{
		Name:                p.name,
		APIKey:              p.apiKey,
		BaseURL:             p.baseURL,
		HTTPClient:          p.httpClient,
		NativeJSON:          true,
		EmbedBatch:          p.embedBatch,
		MaxTokensParam:      p.maxTokensKey,
		OmitEmptyAuth:       true,
		Headers:             p.headers,
		TranscriptionFormat: p.sttFormat,

		NullAssistantContent: p.nullContent,
		LenientStream:        p.lenient,
	}
}

// Chat returns a streaming-capable provider.LanguageModel for modelID.
func (p *Provider) Chat(modelID string) provider.LanguageModel {
	return openaicompat.NewLanguageModel(p.config(), modelID)
}

// Embedding returns a provider.EmbeddingModel for modelID.
func (p *Provider) Embedding(modelID string) provider.EmbeddingModel {
	return openaicompat.NewEmbeddingModel(p.config(), modelID)
}

// Speech returns a provider.SpeechModel for modelID (/audio/speech). Voice
// and model strings are passed through verbatim, so any server-defined
// voice works.
func (p *Provider) Speech(modelID string) provider.SpeechModel {
	return openaicompat.NewSpeechModel(p.config(), modelID)
}

// Transcription returns a provider.TranscriptionModel for modelID
// (/audio/transcriptions), e.g. "MediaTek-Research/Breeze-ASR-25".
func (p *Provider) Transcription(modelID string) provider.TranscriptionModel {
	return openaicompat.NewTranscriptionModel(p.config(), modelID)
}

// ModelLister returns a provider.ModelLister over GET {base}/models, for
// ai.ListModels. A ModelListCall.OutputModality is sent as OpenRouter's
// output_modalities query parameter and also applied client-side; servers
// that report no modalities (vLLM, Ollama) are not filtered.
func (p *Provider) ModelLister() provider.ModelLister {
	return openaicompat.NewModelLister(p.config(), "output_modalities")
}

// KeyChecker returns a provider.KeyChecker for the configured API key, for
// ai.CheckKey. It reads the key-info endpoint (WithKeyInfoPath, default
// OpenRouter's "/key"), which reports the key's label, limit and usage; a
// server without one (404 or 405) is asked for GET {base}/models instead,
// which proves the key only where /models requires authentication.
func (p *Provider) KeyChecker() provider.KeyChecker {
	return openaicompat.NewKeyChecker(p.config(), p.keyInfoPath)
}
