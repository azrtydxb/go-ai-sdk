package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultVoiceID is ElevenLabs' documented default voice, "Rachel".
const defaultVoiceID = "21m00Tcm4TlvDq8ikWAM"

type speechModel struct {
	provider *Provider
	modelID  string
}

func (m *speechModel) ModelID() string      { return m.modelID }
func (m *speechModel) ProviderName() string { return providerName }

// ---- wire types ----

type speechRequest struct {
	Text         string `json:"text"`
	ModelID      string `json:"model_id"`
	LanguageCode string `json:"language_code,omitempty"`
}

// outputFormatWire maps a provider.SpeechCall.OutputFormat value to the
// ElevenLabs output_format query value and the resulting audio MediaType.
func outputFormatWire(format string) (wireFormat, mediaType string) {
	switch format {
	case "mp3", "":
		return "mp3_44100_128", "audio/mpeg"
	case "pcm":
		return "pcm_44100", "audio/pcm"
	case "ulaw":
		return "ulaw_8000", "audio/basic"
	default:
		return format, "application/octet-stream"
	}
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	voice := call.Voice
	if voice == "" {
		voice = defaultVoiceID
	}
	wireFormat, mediaType := outputFormatWire(call.OutputFormat)

	req := speechRequest{
		Text:         call.Text,
		ModelID:      m.modelID,
		LanguageCode: call.Language,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: marshal speech request: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/text-to-speech/" + url.PathEscape(voice) +
		"?output_format=" + url.QueryEscape(wireFormat)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: build speech request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("xi-api-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: read speech response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	return &provider.SpeechResponse{
		Audio:     body,
		MediaType: mediaType,
	}, nil
}
