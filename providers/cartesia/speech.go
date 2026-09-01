package cartesia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/providerutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// cartesiaAuthHeader is the HTTP header carrying the API key; extra headers
// from provider.SpeechCall.Headers must not be able to override it.
const cartesiaAuthHeader = "Authorization"

// speechModel implements provider.SpeechModel against Cartesia's
// text-to-speech API.
type speechModel struct {
	provider *Provider
	modelID  string
}

func (m *speechModel) ModelID() string      { return m.modelID }
func (m *speechModel) ProviderName() string { return providerName }

// ---- wire types ----

type voiceWire struct {
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

// outputFormatWire is Cartesia's discriminated-union output_format shape.
// The "mp3" container takes {"container","sample_rate","bit_rate"} — NO
// "encoding" field, since MP3 is itself a fixed encoding. The "wav" and
// "raw" containers take {"container","encoding","sample_rate"} and carry no
// bit_rate. Encoding/BitRate are omitted per-container by leaving the
// unused field's zero value out via omitempty.
type outputFormatWire struct {
	Container  string `json:"container"`
	Encoding   string `json:"encoding,omitempty"`
	SampleRate int    `json:"sample_rate"`
	BitRate    int    `json:"bit_rate,omitempty"`
}

type speechRequest struct {
	ModelID      string           `json:"model_id"`
	Transcript   string           `json:"transcript"`
	Voice        voiceWire        `json:"voice"`
	OutputFormat outputFormatWire `json:"output_format"`
	Language     string           `json:"language,omitempty"`
}

// mediaTypeForContainer maps a Cartesia output_format container to the
// resulting audio MediaType.
func mediaTypeForContainer(container string) string {
	switch container {
	case "mp3", "":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// encodingForContainer maps a Cartesia output_format container to its
// default encoding. Not applicable to "mp3", which has no encoding field
// (see outputFormatForContainer).
func encodingForContainer(container string) string {
	switch container {
	case "mp3", "":
		return "mp3"
	case "wav":
		return "pcm_s16le"
	default:
		return "pcm_f32le"
	}
}

const (
	defaultSampleRate = 44100
	defaultBitRate    = 128000
)

// outputFormatForContainer builds Cartesia's discriminated-union
// output_format object for container: "mp3" sends {container, sample_rate,
// bit_rate} with no "encoding" field; "wav" and "raw" send
// {container, encoding, sample_rate} with no "bit_rate" field.
func outputFormatForContainer(container string) outputFormatWire {
	if container == "mp3" || container == "" {
		c := container
		if c == "" {
			c = "mp3"
		}
		return outputFormatWire{
			Container:  c,
			SampleRate: defaultSampleRate,
			BitRate:    defaultBitRate,
		}
	}
	return outputFormatWire{
		Container:  container,
		Encoding:   encodingForContainer(container),
		SampleRate: defaultSampleRate,
	}
}

func (m *speechModel) GenerateSpeech(ctx context.Context, call provider.SpeechCall) (*provider.SpeechResponse, error) {
	if call.Voice == "" {
		return nil, errors.New("cartesia: Voice is required")
	}

	container := call.OutputFormat
	if container == "" {
		container = "mp3"
	}

	req := speechRequest{
		ModelID:    m.modelID,
		Transcript: call.Text,
		Voice: voiceWire{
			Mode: "id",
			ID:   call.Voice,
		},
		OutputFormat: outputFormatForContainer(container),
		Language:     call.Language,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cartesia: marshal speech request: %w", err)
	}
	reqBody, err = providerutil.ApplyProviderOptions(reqBody, call.ProviderOptions, "cartesia")
	if err != nil {
		return nil, fmt.Errorf("cartesia: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/tts/bytes"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("cartesia: build speech request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(cartesiaAuthHeader, "Bearer "+m.provider.apiKey)
	httpReq.Header.Set("Cartesia-Version", cartesiaVersion)
	httpheader.Apply(httpReq, call.Headers, cartesiaAuthHeader)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cartesia: read speech response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	return &provider.SpeechResponse{
		Audio:     body,
		MediaType: mediaTypeForContainer(container),
	}, nil
}
