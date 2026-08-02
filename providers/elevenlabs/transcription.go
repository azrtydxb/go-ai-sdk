package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

type transcriptionModel struct {
	provider *Provider
	modelID  string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return providerName }

// ---- wire types ----

type transcriptionResponse struct {
	Text         string     `json:"text"`
	LanguageCode string     `json:"language_code"`
	Words        []wireWord `json:"words"`
}

type wireWord struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Type  string  `json:"type"`
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fileHeader := make(map[string][]string)
	fileHeader["Content-Disposition"] = []string{`form-data; name="file"; filename="audio"`}
	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fileHeader["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(fileHeader)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: create transcription file part: %w", err)
	}
	if _, err := part.Write(call.Audio); err != nil {
		return nil, fmt.Errorf("elevenlabs: write transcription file part: %w", err)
	}

	if err := mw.WriteField("model_id", m.modelID); err != nil {
		return nil, fmt.Errorf("elevenlabs: write model_id field: %w", err)
	}
	if call.Language != "" {
		if err := mw.WriteField("language_code", call.Language); err != nil {
			return nil, fmt.Errorf("elevenlabs: write language_code field: %w", err)
		}
	}

	if err := applyProviderOptionsForm(mw, call.ProviderOptions); err != nil {
		return nil, fmt.Errorf("elevenlabs: apply provider options: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("elevenlabs: close multipart writer: %w", err)
	}

	reqURL := m.provider.baseURL + "/v1/speech-to-text"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &buf)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: build transcription request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("xi-api-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("elevenlabs: read transcription response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr transcriptionResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("elevenlabs: decode transcription response: %w", err)
	}

	var segments []provider.TranscriptSegment
	for _, w := range wr.Words {
		if w.Type == "word" {
			segments = append(segments, provider.TranscriptSegment{
				Text:     w.Text,
				StartSec: w.Start,
				EndSec:   w.End,
			})
		}
	}
	var duration float64
	if n := len(segments); n > 0 {
		duration = segments[n-1].EndSec
	}

	return &provider.TranscriptionResponse{
		Text:        wr.Text,
		Segments:    segments,
		Language:    wr.LanguageCode,
		DurationSec: duration,
		Raw:         json.RawMessage(body),
	}, nil
}
