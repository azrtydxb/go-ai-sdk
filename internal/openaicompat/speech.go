package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewSpeechModel returns a provider.SpeechModel that speaks the OpenAI
// audio/speech wire format against cfg.
func NewSpeechModel(cfg Config, modelID string) provider.SpeechModel {
	return &speechModel{cfg: cfg, modelID: modelID}
}

type speechModel struct {
	cfg     Config
	modelID string
}

func (m *speechModel) ModelID() string      { return m.modelID }
func (m *speechModel) ProviderName() string { return m.cfg.Name }

// ---- wire types ----

type speechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          string   `json:"voice"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
}

// speechMediaTypes maps a response_format value to the MIME type of the
// returned audio bytes.
var speechMediaTypes = map[string]string{
	"mp3":  "audio/mpeg",
	"wav":  "audio/wav",
	"opus": "audio/opus",
	"aac":  "audio/aac",
	"flac": "audio/flac",
	"pcm":  "audio/pcm",
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	if m.cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", m.cfg.Name)
	}

	voice := call.Voice
	if voice == "" {
		// OpenAI requires a voice; default to "alloy" when none is set.
		voice = "alloy"
	}
	format := call.OutputFormat
	if format == "" {
		format = "mp3"
	}

	req := speechRequest{
		Model:          m.modelID,
		Input:          call.Text,
		Voice:          voice,
		ResponseFormat: format,
		Speed:          call.Speed,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: marshal speech request: %w", err)
	}

	url := m.cfg.BaseURL + "/audio/speech"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build speech request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	m.cfg.setAuthHeader(httpReq)

	resp, err := m.cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read speech response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	mediaType := speechMediaTypes[format]
	if mediaType == "" {
		mediaType = "audio/mpeg"
	}

	return &provider.SpeechResponse{
		Audio:     body,
		MediaType: mediaType,
	}, nil
}
