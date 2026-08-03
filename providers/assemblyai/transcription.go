package assemblyai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// transcriptionModel implements provider.TranscriptionModel against
// AssemblyAI's three-endpoint asynchronous transcription flow: upload the
// audio, create a transcript from the resulting URL, then poll the
// transcript until it reaches a terminal state.
type transcriptionModel struct {
	provider *Provider
	modelID  string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return providerName }

// ---- wire types ----

type uploadResponse struct {
	UploadURL string `json:"upload_url"`
}

type createRequest struct {
	AudioURL     string `json:"audio_url"`
	SpeechModel  string `json:"speech_model,omitempty"`
	LanguageCode string `json:"language_code,omitempty"`
}

type createResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// transcriptResponse matches AssemblyAI's transcript object, returned by
// both the create call and by polling GET .../transcript/{id}.
type transcriptResponse struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	Text          string     `json:"text"`
	Words         []wireWord `json:"words"`
	LanguageCode  string     `json:"language_code"`
	AudioDuration float64    `json:"audio_duration"`
	Error         string     `json:"error"`
}

// wireWord's Start and End are in milliseconds.
type wireWord struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	// call.Prompt is not supported by AssemblyAI's transcription API and is
	// silently ignored.

	uploadURL, err := m.upload(ctx, call)
	if err != nil {
		return nil, err
	}

	id, err := m.create(ctx, call, uploadURL)
	if err != nil {
		return nil, err
	}

	tr, rawBody, err := m.poll(ctx, id)
	if err != nil {
		return nil, err
	}

	segments := make([]provider.TranscriptSegment, 0, len(tr.Words))
	for _, w := range tr.Words {
		segments = append(segments, provider.TranscriptSegment{
			Text:     w.Text,
			StartSec: w.Start / 1000,
			EndSec:   w.End / 1000,
		})
	}

	return &provider.TranscriptionResponse{
		Text:        tr.Text,
		Segments:    segments,
		Language:    tr.LanguageCode,
		DurationSec: tr.AudioDuration,
		Raw:         json.RawMessage(rawBody),
	}, nil
}

// upload sends the raw audio bytes to AssemblyAI's /v2/upload endpoint,
// returning the resulting temporary upload URL.
func (m *transcriptionModel) upload(ctx context.Context, call provider.TranscriptionCall) (string, error) {
	reqURL := m.provider.baseURL + "/v2/upload"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(call.Audio))
	if err != nil {
		return "", fmt.Errorf("assemblyai: build upload request: %w", err)
	}
	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("authorization", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("assemblyai: read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apiError(resp, body)
	}

	var ur uploadResponse
	if err := json.Unmarshal(body, &ur); err != nil {
		return "", fmt.Errorf("assemblyai: decode upload response: %w", err)
	}
	if ur.UploadURL == "" {
		return "", fmt.Errorf("assemblyai: upload response contained no upload_url: %s", body)
	}
	return ur.UploadURL, nil
}

// create creates a transcript from uploadURL via AssemblyAI's
// /v2/transcript endpoint, returning the transcript id.
func (m *transcriptionModel) create(ctx context.Context, call provider.TranscriptionCall, uploadURL string) (string, error) {
	req := createRequest{
		AudioURL:     uploadURL,
		SpeechModel:  m.modelID,
		LanguageCode: call.Language,
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("assemblyai: marshal transcript request: %w", err)
	}
	reqBody, err = applyProviderOptions(reqBody, call.ProviderOptions)
	if err != nil {
		return "", fmt.Errorf("assemblyai: apply provider options: %w", err)
	}

	reqURL := m.provider.baseURL + "/v2/transcript"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("assemblyai: build transcript request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("authorization", m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("assemblyai: read transcript response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apiError(resp, body)
	}

	var created createResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("assemblyai: decode transcript response: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("assemblyai: response contained no transcript id: %s", body)
	}
	return created.ID, nil
}

// poll repeatedly fetches the transcript status until it reaches a
// terminal state ("completed" or "error"), sleeping p.provider.poll()
// between requests. The sleep is ctx-aware: cancellation returns
// ctx.Err() immediately instead of waiting out the interval.
func (m *transcriptionModel) poll(ctx context.Context, id string) (*transcriptResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/v2/transcript/" + id

	// Poll immediately on entry (a transcript may already be complete by
	// the time we ask), then sleep p.provider.poll() between each
	// subsequent attempt — the sleep only runs after a non-terminal
	// response, never before the first poll.
	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("assemblyai: build poll request: %w", err)
		}
		httpReq.Header.Set("authorization", m.provider.apiKey)

		resp, err := m.provider.client().Do(httpReq)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("assemblyai: read poll response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, nil, apiError(resp, body)
		}

		var tr transcriptResponse
		if err := json.Unmarshal(body, &tr); err != nil {
			return nil, nil, fmt.Errorf("assemblyai: decode poll response: %w", err)
		}

		switch tr.Status {
		case "completed":
			return &tr, body, nil
		case "error":
			if tr.Error != "" {
				return nil, nil, fmt.Errorf("assemblyai: transcript %s failed: %s", id, tr.Error)
			}
			return nil, nil, fmt.Errorf("assemblyai: transcript %s failed", id)
		}

		if err := sleep(ctx, m.provider.poll()); err != nil {
			return nil, nil, err
		}
	}
}

// sleep blocks for d or until ctx is done, whichever comes first,
// returning ctx.Err() in the latter case.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
