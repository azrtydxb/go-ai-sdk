package revai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/azrtydxb/go-ai-sdk/provider"
)

// transcriptionModel implements provider.TranscriptionModel against Rev.ai's
// asynchronous transcription flow: create a job from the uploaded audio,
// poll the job until it reaches a terminal state, then fetch the
// transcript.
type transcriptionModel struct {
	provider *Provider
	modelID  string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return providerName }

// ---- wire types ----

type jobResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FailureDetail string `json:"failure_detail"`
}

// transcriptResponse matches the body returned by Rev.ai's transcript
// endpoint when requested with the
// "Accept: application/vnd.rev.transcript.v1.0+json" header.
type transcriptResponse struct {
	Monologues []monologueWire `json:"monologues"`
}

type monologueWire struct {
	Elements []elementWire `json:"elements"`
}

// elementWire's Type is "text" (spoken words, carries Ts/EndTs), "punct"
// (punctuation/whitespace, no timestamps), or "unknown" (unintelligible
// speech, no timestamps).
type elementWire struct {
	Type  string   `json:"type"`
	Value string   `json:"value"`
	Ts    *float64 `json:"ts,omitempty"`
	EndTs *float64 `json:"end_ts,omitempty"`
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	// call.Prompt is not supported by Rev.ai's transcription API and is
	// silently ignored.

	id, err := m.createJob(ctx, call)
	if err != nil {
		return nil, err
	}

	if err := m.pollJob(ctx, id); err != nil {
		return nil, err
	}

	tr, rawBody, err := m.fetchTranscript(ctx, id)
	if err != nil {
		return nil, err
	}

	var text string
	var segments []provider.TranscriptSegment
	for _, mono := range tr.Monologues {
		for _, el := range mono.Elements {
			switch el.Type {
			case "text", "punct":
				text += el.Value
			}
			// "unknown"-type elements (unintelligible speech) are
			// intentionally omitted from both Text and Segments: they carry
			// no usable Value and no timestamps.
			if el.Type == "text" {
				seg := provider.TranscriptSegment{Text: el.Value}
				if el.Ts != nil {
					seg.StartSec = *el.Ts
				}
				if el.EndTs != nil {
					seg.EndSec = *el.EndTs
				}
				segments = append(segments, seg)
			}
		}
	}

	var duration float64
	if n := len(segments); n > 0 {
		duration = segments[n-1].EndSec
	}

	return &provider.TranscriptionResponse{
		Text:        text,
		Segments:    segments,
		DurationSec: duration,
		Raw:         json.RawMessage(rawBody),
	}, nil
}

// createJob submits the audio to Rev.ai's /speechtotext/v1/jobs endpoint as
// a multipart request: a "media" file part plus an "options" JSON part
// carrying {"language":...}. Unlike most providers in this SDK,
// ProviderOptions["revai"] is merged into that nested "options" JSON object
// rather than top-level into the request, because the request itself has
// no other body — the "options" part *is* the job configuration Rev.ai
// reads.
func (m *transcriptionModel) createJob(ctx context.Context, call provider.TranscriptionCall) (string, error) {
	optsBody, err := buildJobOptions(call)
	if err != nil {
		return "", fmt.Errorf("revai: build job options: %w", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	contentType := call.MediaType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := "audio" + extForMediaType(call.MediaType)

	mediaHeader := make(map[string][]string)
	mediaHeader["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="media"; filename=%q`, filename)}
	mediaHeader["Content-Type"] = []string{contentType}
	mediaPart, err := mw.CreatePart(mediaHeader)
	if err != nil {
		return "", fmt.Errorf("revai: create media part: %w", err)
	}
	if _, err := mediaPart.Write(call.Audio); err != nil {
		return "", fmt.Errorf("revai: write media part: %w", err)
	}

	optsHeader := make(map[string][]string)
	optsHeader["Content-Disposition"] = []string{`form-data; name="options"`}
	optsHeader["Content-Type"] = []string{"application/json"}
	optsPart, err := mw.CreatePart(optsHeader)
	if err != nil {
		return "", fmt.Errorf("revai: create options part: %w", err)
	}
	if _, err := optsPart.Write(optsBody); err != nil {
		return "", fmt.Errorf("revai: write options part: %w", err)
	}

	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("revai: close multipart writer: %w", err)
	}

	reqURL := m.provider.baseURL + "/speechtotext/v1/jobs"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &buf)
	if err != nil {
		return "", fmt.Errorf("revai: build job request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("revai: read job response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", apiError(resp, body)
	}

	var jr jobResponse
	if err := json.Unmarshal(body, &jr); err != nil {
		return "", fmt.Errorf("revai: decode job response: %w", err)
	}
	if jr.ID == "" {
		return "", fmt.Errorf("revai: response contained no job id: %s", body)
	}
	return jr.ID, nil
}

// buildJobOptions builds the JSON body for the "options" multipart part:
// {"language":call.Language} (omitted when empty), with
// call.ProviderOptions["revai"] merged in on top, entries from the option
// map winning over whatever the SDK built.
func buildJobOptions(call provider.TranscriptionCall) ([]byte, error) {
	base := map[string]any{}
	if call.Language != "" {
		base["language"] = call.Language
	}
	if opts, ok := call.ProviderOptions["revai"].(map[string]any); ok {
		for k, v := range opts {
			base[k] = v
		}
	}
	return json.Marshal(base)
}

// pollJob repeatedly fetches the job status until it reaches a terminal
// state ("transcribed" or "failed"), sleeping p.provider.poll() between
// requests. The sleep is ctx-aware: cancellation returns ctx.Err()
// immediately instead of waiting out the interval.
func (m *transcriptionModel) pollJob(ctx context.Context, id string) error {
	reqURL := m.provider.baseURL + "/speechtotext/v1/jobs/" + id

	// Poll immediately on entry (a job may already be transcribed by the
	// time we ask), then sleep p.provider.poll() between each subsequent
	// attempt — the sleep only runs after a non-terminal response, never
	// before the first poll.
	for {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return fmt.Errorf("revai: build poll request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)

		resp, err := m.provider.client().Do(httpReq)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("revai: read poll response: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return apiError(resp, body)
		}

		var jr jobResponse
		if err := json.Unmarshal(body, &jr); err != nil {
			return fmt.Errorf("revai: decode poll response: %w", err)
		}

		switch jr.Status {
		case "transcribed":
			return nil
		case "failed":
			if jr.FailureDetail != "" {
				return fmt.Errorf("revai: job %s failed: %s", id, jr.FailureDetail)
			}
			return fmt.Errorf("revai: job %s failed", id)
		}

		if err := sleep(ctx, m.provider.poll()); err != nil {
			return err
		}
	}
}

// fetchTranscript fetches the job transcript via
// GET .../jobs/{id}/transcript with
// "Accept: application/vnd.rev.transcript.v1.0+json" so Rev.ai returns the
// structured JSON transcript instead of plain text.
func (m *transcriptionModel) fetchTranscript(ctx context.Context, id string) (*transcriptResponse, []byte, error) {
	reqURL := m.provider.baseURL + "/speechtotext/v1/jobs/" + id + "/transcript"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("revai: build transcript request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.provider.apiKey)
	httpReq.Header.Set("Accept", "application/vnd.rev.transcript.v1.0+json")

	resp, err := m.provider.client().Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("revai: read transcript response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, apiError(resp, body)
	}

	var tr transcriptResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, nil, fmt.Errorf("revai: decode transcript response: %w", err)
	}
	return &tr, body, nil
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
