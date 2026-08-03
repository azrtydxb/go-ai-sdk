package hume

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// speechModel implements provider.SpeechModel against Hume's Octave
// text-to-speech API.
//
// Note: call.Language has no equivalent in Hume's /v0/tts wire format and
// is silently ignored.
type speechModel struct {
	provider *Provider
	modelID  string
}

func (m *speechModel) ModelID() string      { return m.modelID }
func (m *speechModel) ProviderName() string { return providerName }

// ---- wire types ----

type speechRequest struct {
	Utterances []utteranceWire `json:"utterances"`
	Format     formatWire      `json:"format"`
}

type utteranceWire struct {
	Text  string     `json:"text"`
	Voice *voiceWire `json:"voice,omitempty"`
	Speed *float64   `json:"speed,omitempty"`
}

type voiceWire struct {
	Name string `json:"name"`
}

type formatWire struct {
	Type string `json:"type"`
}

type speechResponseWire struct {
	Generations []generationWire `json:"generations"`
}

type generationWire struct {
	Audio string `json:"audio"`
}

// mediaTypeForFormat maps a provider.SpeechCall.OutputFormat value to the
// resulting audio MediaType.
func mediaTypeForFormat(format string) string {
	switch format {
	case "mp3", "":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/pcm"
	default:
		return "application/octet-stream"
	}
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	format := call.OutputFormat
	if format == "" {
		format = "mp3"
	}

	utterance := utteranceWire{Text: call.Text}
	if call.Voice != "" {
		utterance.Voice = &voiceWire{Name: call.Voice}
	}
	if call.Speed != nil {
		utterance.Speed = call.Speed
	}

	req := speechRequest{
		Utterances: []utteranceWire{utterance},
		Format:     formatWire{Type: format},
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("hume: marshal speech request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return nil, fmt.Errorf("hume: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v0/tts"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("hume: build speech request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Hume-Api-Key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hume: read speech response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wire speechResponseWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("hume: unmarshal speech response: %w", err)
	}
	if len(wire.Generations) == 0 || wire.Generations[0].Audio == "" {
		return nil, errors.New("hume: speech response contained no audio generations")
	}

	audio, err := base64.StdEncoding.DecodeString(wire.Generations[0].Audio)
	if err != nil {
		return nil, fmt.Errorf("hume: decode base64 audio: %w", err)
	}

	return &provider.SpeechResponse{
		Audio:     audio,
		MediaType: mediaTypeForFormat(format),
	}, nil
}
