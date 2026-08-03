package gladia

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

// transcriptionModel implements provider.TranscriptionModel against
// Gladia's three-endpoint asynchronous transcription flow: upload the
// audio, create a pre-recorded transcription job from the resulting URL,
// then poll the job until it reaches a terminal state.
type transcriptionModel struct {
	provider *Provider
	modelID  string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return providerName }

// ---- wire types ----

type uploadResponse struct {
	AudioURL string `json:"audio_url"`
}

type languageConfig struct {
	Languages []string `json:"languages"`
}

type createRequest struct {
	AudioURL       string          `json:"audio_url"`
	LanguageConfig *languageConfig `json:"language_config,omitempty"`
}

type createResponse struct {
	ID string `json:"id"`
	// ResultURL is intentionally unused: this provider polls the job by ID
	// (GET .../pre-recorded/{id}) rather than fetching ResultURL directly,
	// so the field is decoded for documentation purposes only.
	ResultURL string `json:"result_url"`
}

// pollResponse matches Gladia's pre-recorded transcription job object,
// returned by polling GET .../pre-recorded/{id}.
type pollResponse struct {
	ID        string      `json:"id"`
	Status    string      `json:"status"`
	ErrorCode any         `json:"error_code"`
	Result    *resultWire `json:"result"`
}

type resultWire struct {
	Metadata      *metadataWire      `json:"metadata"`
	Transcription *transcriptionWire `json:"transcription"`
}

type metadataWire struct {
	AudioDuration float64 `json:"audio_duration"`
}

type transcriptionWire struct {
	FullTranscript string          `json:"full_transcript"`
	Utterances     []utteranceWire `json:"utterances"`
}

// utteranceWire's Start and End are already in seconds.
type utteranceWire struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	// call.Prompt is not supported by Gladia's transcription API and is
	// silently ignored.

	audioURL, err := m.upload(ctx, call)
	if err != nil {
		return nil, err
	}

	id, err := m.create(ctx, call, audioURL)
	if err != nil {
		return nil, err
	}

	pr, rawBody, err := m.poll(ctx, id)
	if err != nil {
		return nil, err
	}

	var text string
	var segments []provider.TranscriptSegment
	var duration float64
	if pr.Result != nil {
		if pr.Result.Transcription != nil {
			text = pr.Result.Transcription.FullTranscript
			for _, u := range pr.Result.Transcription.Utterances {
				segments = append(segments, provider.TranscriptSegment{
					Text:     u.Text,
					StartSec: u.Start,
					EndSec:   u.End,
				})
			}
		}
		if pr.Result.Metadata != nil {
			duration = pr.Result.Metadata.AudioDuration
		}
	}

	return &provider.TranscriptionResponse{
		Text:        text,
		Segments:    segments,
		DurationSec: duration,
		Raw:         json.RawMessage(rawBody),
	}, nil
}

// upload sends the raw audio bytes to Gladia's /v2/upload endpoint as a
// multipart "audio" field, returning the resulting audio_url.
func (m *transcriptionModel) upload(ctx context.Context, call provider.TranscriptionCall) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := "audio" + transcribeutil.ExtForMediaType(call.MediaType)

	partHeader := make(map[string][]string)
	partHeader["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="audio"; filename=%q`, filename)}
	partHeader["Content-Type"] = []string{contentType}
	part, err := mw.CreatePart(partHeader)
	if err != nil {
		return "", fmt.Errorf("gladia: create upload file part: %w", err)
	}
	if _, err := part.Write(call.Audio); err != nil {
		return "", fmt.Errorf("gladia: write upload file part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("gladia: close multipart writer: %w", err)
	}

	reqURL := m.provider.baseURL + "/v2/upload"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &buf)
	if err != nil {
		return "", fmt.Errorf("gladia: build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("x-gladia-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("gladia: read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apiError(resp, body)
	}

	var ur uploadResponse
	if err := json.Unmarshal(body, &ur); err != nil {
		return "", fmt.Errorf("gladia: decode upload response: %w", err)
	}
	if ur.AudioURL == "" {
		return "", fmt.Errorf("gladia: upload response contained no audio_url: %s", body)
	}
	return ur.AudioURL, nil
}

// create creates a pre-recorded transcription job from audioURL via
// Gladia's /v2/pre-recorded endpoint, returning the job id.
func (m *transcriptionModel) create(ctx context.Context, call provider.TranscriptionCall, audioURL string) (string, error) {
	req := createRequest{AudioURL: audioURL}
	if call.Language != "" {
		req.LanguageConfig = &languageConfig{Languages: []string{call.Language}}
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("gladia: marshal pre-recorded request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return "", fmt.Errorf("gladia: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v2/pre-recorded"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("gladia: build pre-recorded request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-gladia-key", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("gladia: read pre-recorded response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apiError(resp, body)
	}

	var created createResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("gladia: decode pre-recorded response: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("gladia: response contained no job id: %s", body)
	}
	return created.ID, nil
}

// poll repeatedly fetches the job status until it reaches a terminal state
// ("done" or "error"), sleeping p.provider.poll() between requests. The
// sleep is ctx-aware: cancellation returns ctx.Err() immediately instead of
// waiting out the interval.
func (m *transcriptionModel) poll(ctx context.Context, id string) (*pollResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/v2/pre-recorded/" + id

	// Poll immediately on entry (a job may already be done by the time we
	// ask), then sleep p.provider.poll() between each subsequent attempt —
	// the sleep only runs after a non-terminal response, never before the
	// first poll.
	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("gladia: build poll request: %w", err)
		}
		httpReq.Header.Set("x-gladia-key", m.provider.apiKey)

		resp, err := m.provider.client().Do(httpReq)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("gladia: read poll response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, apiError(resp, body)
		}

		var pr pollResponse
		if err := json.Unmarshal(body, &pr); err != nil {
			return nil, nil, fmt.Errorf("gladia: decode poll response: %w", err)
		}

		switch pr.Status {
		case "done":
			return &pr, body, nil
		case "error":
			if pr.ErrorCode != nil {
				return nil, nil, fmt.Errorf("gladia: transcription %s failed: %v", id, pr.ErrorCode)
			}
			// The "error_code" field was empty despite a terminal error
			// status; fall back to the raw response body so the failure
			// isn't silently reported with no detail at all.
			return nil, nil, fmt.Errorf("gladia: transcription %s failed: %s", id, body)
		}

		if err := transcribeutil.Sleep(ctx, m.provider.poll()); err != nil {
			return nil, nil, err
		}
	}
}
