package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/transcribeutil"
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
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	ext := transcribeutil.ExtForMediaType(call.MediaType)
	if ext == "" {
		ext = ".bin"
	}
	filename := "audio" + ext
	fileHeader := make(map[string][]string)
	fileHeader["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)}
	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fileHeader["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(fileHeader)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: create translation file part: %w", err)
	}
	if _, err := part.Write(call.Audio); err != nil {
		return nil, fmt.Errorf("openaicompat: write translation file part: %w", err)
	}

	if err := mw.WriteField("model", m.modelID); err != nil {
		return nil, fmt.Errorf("openaicompat: write model field: %w", err)
	}
	if call.Prompt != "" {
		if err := mw.WriteField("prompt", call.Prompt); err != nil {
			return nil, fmt.Errorf("openaicompat: write prompt field: %w", err)
		}
	}
	if err := mw.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("openaicompat: write response_format field: %w", err)
	}

	if err := applyProviderOptionsForm(mw, call.ProviderOptions, m.cfg.Name); err != nil {
		return nil, fmt.Errorf("openaicompat: apply provider options: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("openaicompat: close multipart writer: %w", err)
	}

	url := m.cfg.BaseURL + "/audio/translations"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build translation request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	m.cfg.setAuthHeader(httpReq)

	resp, err := m.cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read translation response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
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
