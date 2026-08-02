package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewTranscriptionModel returns a provider.TranscriptionModel that speaks
// the OpenAI audio/transcriptions wire format against cfg.
func NewTranscriptionModel(cfg Config, modelID string) provider.TranscriptionModel {
	return &transcriptionModel{cfg: cfg, modelID: modelID}
}

type transcriptionModel struct {
	cfg     Config
	modelID string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return m.cfg.Name }

// transcriptionExtensions maps a MediaType to the upload filename extension
// used for the multipart file part.
var transcriptionExtensions = map[string]string{
	"audio/mpeg": "mp3",
	"audio/wav":  "wav",
	"audio/mp4":  "mp4",
	"audio/webm": "webm",
}

func transcriptionExtension(mediaType string) string {
	if ext, ok := transcriptionExtensions[mediaType]; ok {
		return ext
	}
	return "bin"
}

// transcriptionResponseFormat picks the response_format value: models
// containing "gpt-4o" reject verbose_json, so those get the plain "json"
// shape (text only); everything else (whisper-1, etc.) gets "verbose_json"
// (text/language/duration/segments).
func transcriptionResponseFormat(modelID string) string {
	if strings.Contains(modelID, "gpt-4o") {
		return "json"
	}
	return "verbose_json"
}

// ---- wire types ----

type transcriptionResponse struct {
	Text     string                     `json:"text"`
	Language string                     `json:"language,omitempty"`
	Duration float64                    `json:"duration,omitempty"`
	Segments []transcriptionSegmentWire `json:"segments,omitempty"`
}

type transcriptionSegmentWire struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := "audio." + transcriptionExtension(call.MediaType)
	fileHeader := make(map[string][]string)
	fileHeader["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, filename)}
	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fileHeader["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(fileHeader)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: create transcription file part: %w", err)
	}
	if _, err := part.Write(call.Audio); err != nil {
		return nil, fmt.Errorf("openaicompat: write transcription file part: %w", err)
	}

	if err := mw.WriteField("model", m.modelID); err != nil {
		return nil, fmt.Errorf("openaicompat: write model field: %w", err)
	}
	if call.Language != "" {
		if err := mw.WriteField("language", call.Language); err != nil {
			return nil, fmt.Errorf("openaicompat: write language field: %w", err)
		}
	}
	if call.Prompt != "" {
		if err := mw.WriteField("prompt", call.Prompt); err != nil {
			return nil, fmt.Errorf("openaicompat: write prompt field: %w", err)
		}
	}
	responseFormat := transcriptionResponseFormat(m.modelID)
	if err := mw.WriteField("response_format", responseFormat); err != nil {
		return nil, fmt.Errorf("openaicompat: write response_format field: %w", err)
	}

	if err := applyProviderOptionsForm(mw, call.ProviderOptions, m.cfg.Name); err != nil {
		return nil, fmt.Errorf("openaicompat: apply provider options: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("openaicompat: close multipart writer: %w", err)
	}

	url := m.cfg.BaseURL + "/audio/transcriptions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build transcription request: %w", err)
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
		return nil, fmt.Errorf("openaicompat: read transcription response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr transcriptionResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode transcription response: %w", err)
	}

	segments := make([]provider.TranscriptSegment, len(wr.Segments))
	for i, s := range wr.Segments {
		segments[i] = provider.TranscriptSegment{
			Text:     s.Text,
			StartSec: s.Start,
			EndSec:   s.End,
		}
	}

	return &provider.TranscriptionResponse{
		Text:        wr.Text,
		Segments:    segments,
		Language:    wr.Language,
		DurationSec: wr.Duration,
		Raw:         json.RawMessage(body),
	}, nil
}
