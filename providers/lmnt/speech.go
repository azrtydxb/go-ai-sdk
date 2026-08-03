package lmnt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultVoice is LMNT's documented default voice.
const defaultVoice = "leah"

// speechModel implements provider.SpeechModel against the LMNT
// text-to-speech API.
type speechModel struct {
	provider *Provider
	modelID  string
}

func (m *speechModel) ModelID() string      { return m.modelID }
func (m *speechModel) ProviderName() string { return providerName }

// ---- wire types ----

type speechRequest struct {
	Voice    string   `json:"voice"`
	Text     string   `json:"text"`
	Model    string   `json:"model,omitempty"`
	Format   string   `json:"format"`
	Language string   `json:"language,omitempty"`
	Speed    *float64 `json:"speed,omitempty"`
}

// mediaTypeForFormat maps a provider.SpeechCall.OutputFormat value to the
// resulting audio MediaType.
func mediaTypeForFormat(format string) string {
	switch format {
	case "mp3", "":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	voice := call.Voice
	if voice == "" {
		voice = defaultVoice
	}
	format := call.OutputFormat
	if format == "" {
		format = "mp3"
	}

	req := speechRequest{
		Voice:    voice,
		Text:     call.Text,
		Model:    m.modelID,
		Format:   format,
		Language: call.Language,
		Speed:    call.Speed,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("lmnt: marshal speech request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("lmnt: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/ai/speech/bytes"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("lmnt: build speech request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lmnt: read speech response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	return &provider.SpeechResponse{
		Audio:     body,
		MediaType: mediaTypeForFormat(format),
	}, nil
}
