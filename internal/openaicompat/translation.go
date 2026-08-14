package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewTranslationModel returns a provider.TranslationModel that speaks the
// OpenAI audio/translations wire format against cfg. Unlike
// NewTranscriptionModel, OpenAI's translations endpoint always returns
// English text and always accepts response_format=verbose_json (there is
// no gpt-4o-transcribe-style restriction here since only whisper-1
// currently supports translations).
func NewTranslationModel(cfg Config, modelID string) provider.TranslationModel {
	return &translationModel{cfg: cfg, modelID: modelID}
}

type translationModel struct {
	cfg     Config
	modelID string
}

func (m *translationModel) ModelID() string      { return m.modelID }
func (m *translationModel) ProviderName() string { return m.cfg.Name }

// ---- wire types ----

type translationResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

func (m *translationModel) Translate(ctx context.Context, call provider.TranslationCall) (*provider.TranslationResponse, error) {
	body, err := doAudioForm(ctx, m.cfg, m.modelID, audioFormCall{
		endpoint:        "translations",
		kind:            "translation",
		responseFormat:  "verbose_json",
		mediaType:       call.MediaType,
		audio:           call.Audio,
		prompt:          call.Prompt,
		providerOptions: call.ProviderOptions,
		headers:         call.Headers,
	})
	if err != nil {
		return nil, err
	}

	var wr translationResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode translation response: %w", err)
	}

	return &provider.TranslationResponse{
		Text:        wr.Text,
		Language:    wr.Language,
		DurationSec: wr.Duration,
		Raw:         json.RawMessage(body),
	}, nil
}
