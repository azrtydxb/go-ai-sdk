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

// TranscriptionModelProvider is implemented by provider packages that can
// construct a provider.TranscriptionModel for a given model ID.
type TranscriptionModelProvider interface {
	TranscriptionModel(id string) provider.TranscriptionModel
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

// LanguageModel resolves id ("provider:model") into a provider.LanguageModel.
func (r *Registry) LanguageModel(id string) (provider.LanguageModel, error) {
	name, model, err := splitID(id)
	if err != nil {
		return nil, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	lp, ok := p.(LanguageModelProvider)
	if !ok {
		return nil, fmt.Errorf("ai: provider %q does not support language models", name)
	}
	return lp.Model(model), nil
}

// EmbeddingModel resolves id ("provider:model") into a provider.EmbeddingModel.
func (r *Registry) EmbeddingModel(id string) (provider.EmbeddingModel, error) {
	name, model, err := splitID(id)
	if err != nil {
		return nil, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	ep, ok := p.(EmbeddingModelProvider)
	if !ok {
		return nil, fmt.Errorf("ai: provider %q does not support embedding models", name)
	}
	return ep.EmbeddingModel(model), nil
}

// ImageModel resolves id ("provider:model") into a provider.ImageModel.
func (r *Registry) ImageModel(id string) (provider.ImageModel, error) {
	name, model, err := splitID(id)
	if err != nil {
		return nil, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	ip, ok := p.(ImageModelProvider)
	if !ok {
		return nil, fmt.Errorf("ai: provider %q does not support image models", name)
	}
	return ip.ImageModel(model), nil
}

// SpeechModel resolves id ("provider:model") into a provider.SpeechModel.
func (r *Registry) SpeechModel(id string) (provider.SpeechModel, error) {
	name, model, err := splitID(id)
	if err != nil {
		return nil, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	sp, ok := p.(SpeechModelProvider)
	if !ok {
		return nil, fmt.Errorf("ai: provider %q does not support speech models", name)
	}
	return sp.SpeechModel(model), nil
}

// TranscriptionModel resolves id ("provider:model") into a
// provider.TranscriptionModel.
func (r *Registry) TranscriptionModel(id string) (provider.TranscriptionModel, error) {
	name, model, err := splitID(id)
	if err != nil {
		return nil, err
	}
	p, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	tp, ok := p.(TranscriptionModelProvider)
	if !ok {
		return nil, fmt.Errorf("ai: provider %q does not support transcription models", name)
	}
	return tp.TranscriptionModel(model), nil
}
