package ai

import (
	"fmt"
	"strings"
	"sync"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// LanguageModelProvider is implemented by provider packages that can
// construct a provider.LanguageModel for a given model ID.
type LanguageModelProvider interface {
	Model(id string) provider.LanguageModel
}

// EmbeddingModelProvider is implemented by provider packages that can
// construct a provider.EmbeddingModel for a given model ID.
type EmbeddingModelProvider interface {
	EmbeddingModel(id string) provider.EmbeddingModel
}

// ImageModelProvider is implemented by provider packages that can
// construct a provider.ImageModel for a given model ID.
type ImageModelProvider interface {
	ImageModel(id string) provider.ImageModel
}

// SpeechModelProvider is implemented by provider packages that can
// construct a provider.SpeechModel for a given model ID.
type SpeechModelProvider interface {
	SpeechModel(id string) provider.SpeechModel
}

// VideoModelProvider is implemented by provider packages that can
// construct a provider.VideoModel for a given model ID.
type VideoModelProvider interface {
	VideoModel(id string) provider.VideoModel
}

// TranscriptionModelProvider is implemented by provider packages that can
// construct a provider.TranscriptionModel for a given model ID.
type TranscriptionModelProvider interface {
	TranscriptionModel(id string) provider.TranscriptionModel
}

// RerankingModelProvider is implemented by provider packages that can
// construct a provider.RerankingModel for a given model ID.
type RerankingModelProvider interface {
	RerankingModel(id string) provider.RerankingModel
}

// Registry maps provider names to provider values (e.g. *openai.Provider,
// *anthropic.Provider) and resolves "provider:model" IDs into concrete
// provider.LanguageModel / EmbeddingModel / ImageModel / SpeechModel /
// TranscriptionModel values, type-asserting the registered provider
// against the matching capability interface at lookup time.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]any
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]any)}
}

// Register stores p under name. p is typically a provider package's
// *Provider value (e.g. openai.New()); its capabilities (which of
// LanguageModelProvider, EmbeddingModelProvider, etc. it implements) are
// checked lazily at lookup time, so p need not implement every capability
// interface.
func (r *Registry) Register(name string, p any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
}

// splitID splits id into a provider name and model id on the first ':',
// so that model IDs containing ':' (e.g. bedrock's
// "anthropic.claude-3:1") round-trip intact.
func splitID(id string) (name, model string, err error) {
	name, model, ok := strings.Cut(id, ":")
	if !ok {
		return "", "", fmt.Errorf("ai: invalid model id %q (want \"provider:model\")", id)
	}
	return name, model, nil
}

func (r *Registry) lookup(name string) (any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("ai: unknown provider %q", name)
	}
	return p, nil
}

// resolve is the shared parse-id/lookup/type-assert plumbing behind every
// per-capability Registry method: it splits id into provider:model, looks up
// the registered provider, asserts it against capability interface P, and
// constructs the model via get. capability names the capability in the
// "does not support X models" error.
func resolve[P, M any](r *Registry, id, capability string, get func(P, string) M) (M, error) {
	var zero M
	name, model, err := splitID(id)
	if err != nil {
		return zero, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return zero, err
	}
	cp, ok := p.(P)
	if !ok {
		return zero, fmt.Errorf("ai: provider %q does not support %s models", name, capability)
	}
	return get(cp, model), nil
}

// LanguageModel resolves id ("provider:model") into a provider.LanguageModel.
func (r *Registry) LanguageModel(id string) (provider.LanguageModel, error) {
	return resolve(r, id, "language", LanguageModelProvider.Model)
}

// EmbeddingModel resolves id ("provider:model") into a provider.EmbeddingModel.
func (r *Registry) EmbeddingModel(id string) (provider.EmbeddingModel, error) {
	return resolve(r, id, "embedding", EmbeddingModelProvider.EmbeddingModel)
}

// ImageModel resolves id ("provider:model") into a provider.ImageModel.
func (r *Registry) ImageModel(id string) (provider.ImageModel, error) {
	return resolve(r, id, "image", ImageModelProvider.ImageModel)
}

// SpeechModel resolves id ("provider:model") into a provider.SpeechModel.
func (r *Registry) SpeechModel(id string) (provider.SpeechModel, error) {
	return resolve(r, id, "speech", SpeechModelProvider.SpeechModel)
}

// VideoModel resolves id ("provider:model") into a provider.VideoModel.
func (r *Registry) VideoModel(id string) (provider.VideoModel, error) {
	return resolve(r, id, "video", VideoModelProvider.VideoModel)
}

// TranscriptionModel resolves id ("provider:model") into a
// provider.TranscriptionModel.
func (r *Registry) TranscriptionModel(id string) (provider.TranscriptionModel, error) {
	return resolve(r, id, "transcription", TranscriptionModelProvider.TranscriptionModel)
}

// RerankingModel resolves id ("provider:model") into a provider.RerankingModel.
func (r *Registry) RerankingModel(id string) (provider.RerankingModel, error) {
	return resolve(r, id, "reranking", RerankingModelProvider.RerankingModel)
}
