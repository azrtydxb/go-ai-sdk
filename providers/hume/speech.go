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
//
// Note: Hume's voice.provider defaults to CUSTOM_VOICE, so a bare
// call.Voice resolves against the caller's custom voices, not Hume's
// shared Voice Library. Set ProviderOptions["hume"]["voice_provider"] to
// "HUME_AI" to resolve call.Voice as a Voice Library voice instead — this
// SDK-local convenience key is extracted by extractVoiceProvider before
// the generic ProviderOptions merge and never reaches the wire request
// under that name; it only ever affects utterances[0].voice.provider (and
// only when call.Voice is also set).
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
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
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

// extractVoiceProvider pulls the SDK-local convenience key
// ProviderOptions["hume"]["voice_provider"] (a string) out of
// providerOptions, returning its value along with a copy of
// providerOptions that has it removed from the "hume" map.
//
// This key is consumed entirely by the SDK — it's never merged into the
// wire request by applyProviderOptions's generic top-level merge, since
// there is no top-level "voice_provider" field in Hume's wire format. It
// instead controls utterances[0].voice.provider, which is only set when
// GenerateSpeech also has a non-empty call.Voice (Hume's voice.provider is
// meaningless without a voice.name to resolve). This addresses Hume's
// default voice.provider of CUSTOM_VOICE, which resolves Voice against the
// caller's custom voices; set voice_provider to "HUME_AI" to resolve
// Voice as a Hume Voice Library voice instead.
//
// The original providerOptions map (and its "hume" sub-map) are never
// mutated in place — a caller-supplied map may be reused across calls, so
// extractVoiceProvider returns copies instead.
func extractVoiceProvider(providerOptions map[string]any) (voiceProvider string, rest map[string]any) {
	opts, _ := providerOptions["hume"].(map[string]any)
	vp, ok := opts["voice_provider"].(string)
	if !ok || vp == "" {
		return "", providerOptions
	}

	newOpts := make(map[string]any, len(opts))
	for k, v := range opts {
		if k == "voice_provider" {
			continue
		}
		newOpts[k] = v
	}

	newProviderOptions := make(map[string]any, len(providerOptions))
	for k, v := range providerOptions {
		newProviderOptions[k] = v
	}
	newProviderOptions["hume"] = newOpts

	return vp, newProviderOptions
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	format := call.OutputFormat
	if format == "" {
		format = "mp3"
	}

	voiceProvider, providerOptions := extractVoiceProvider(call.ProviderOptions)

	utterance := utteranceWire{Text: call.Text}
	if call.Voice != "" {
		utterance.Voice = &voiceWire{Name: call.Voice}
		if voiceProvider != "" {
			utterance.Voice.Provider = voiceProvider
		}
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
	reqBody, err = applyProviderOptions(reqBody, providerOptions)
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
