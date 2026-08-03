package deepgram

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

// transcriptionModel implements provider.TranscriptionModel against
// Deepgram's /v1/listen endpoint.
type transcriptionModel struct {
	provider *Provider
	modelID  string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return providerName }

// ---- wire types ----

type transcriptionResponseWire struct {
	Metadata wireMetadata `json:"metadata"`
	Results  wireResults  `json:"results"`
}

type wireMetadata struct {
	Duration float64 `json:"duration"`
}

type wireResults struct {
	Channels []wireChannel `json:"channels"`
}

type wireChannel struct {
	Alternatives     []wireAlternative `json:"alternatives"`
	DetectedLanguage string            `json:"detected_language"`
}

type wireAlternative struct {
	Transcript string     `json:"transcript"`
	Words      []wireWord `json:"words"`
}

type wireWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Transcribe sends the audio to Deepgram's /v1/listen endpoint.
//
// Unlike most other providers, Deepgram's request body is the raw audio
// bytes (not JSON, not multipart) — so ProviderOptions["deepgram"] cannot be
// merged into a JSON body. Instead, per Deepgram's API design, those entries
// are added as extra URL query parameters (each value stringified with
// fmt.Sprint). This is a deliberate divergence from the option-merging
// convention used by JSON-bodied providers such as fal and elevenlabs.
func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	// call.Prompt is not supported by Deepgram's /v1/listen endpoint and is
	// silently ignored.

	q := url.Values{}
	q.Set("model", m.modelID)
	q.Set("smart_format", "true")
	if call.Language != "" {
		q.Set("language", call.Language)
	}
	if opts, ok := call.ProviderOptions["deepgram"].(map[string]any); ok {
		for k, v := range opts {
			q.Set(k, fmt.Sprint(v))
		}
	}

	reqURL := m.provider.baseURL + "/v1/listen?" + q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(call.Audio))
	if err != nil {
		return nil, fmt.Errorf("deepgram: build transcription request: %w", err)
	}
	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Authorization", "Token "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("deepgram: read transcription response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr transcriptionResponseWire
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("deepgram: decode transcription response: %w", err)
	}

	if len(wr.Results.Channels) == 0 || len(wr.Results.Channels[0].Alternatives) == 0 {
		return nil, fmt.Errorf("deepgram: response contained no transcription results: %s", body)
	}

	channel := wr.Results.Channels[0]
	alt := channel.Alternatives[0]

	segments := make([]provider.TranscriptSegment, 0, len(alt.Words))
	for _, w := range alt.Words {
		segments = append(segments, provider.TranscriptSegment{
			Text:     w.Word,
			StartSec: w.Start,
			EndSec:   w.End,
		})
	}

	return &provider.TranscriptionResponse{
		Text:        alt.Transcript,
		Segments:    segments,
		Language:    channel.DetectedLanguage,
		DurationSec: wr.Metadata.Duration,
		Raw:         json.RawMessage(body),
	}, nil
}
