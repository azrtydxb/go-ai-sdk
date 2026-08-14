package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
	"github.com/azrtydxb/go-ai-sdk/internal/multipartutil"
	"github.com/azrtydxb/go-ai-sdk/internal/transcribeutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// NewTranscriptionModel returns a provider.TranscriptionModel that speaks
// the OpenAI audio/transcriptions wire format against cfg.
func NewTranscriptionModel(cfg Config, modelID string) provider.TranscriptionModel {
	return &transcriptionModel{cfg: cfg, modelID: modelID}
}

type transcriptionModel struct {
	cfg     Config
	modelID string
}

func (m *transcriptionModel) ModelID() string      { return m.modelID }
func (m *transcriptionModel) ProviderName() string { return m.cfg.Name }

// transcriptionResponseFormat picks the response_format value: models
// containing "gpt-4o" reject verbose_json, so those get the plain "json"
// shape (text only); everything else (whisper-1, etc.) gets "verbose_json"
// (text/language/duration/segments).
func transcriptionResponseFormat(modelID string) string {
	if strings.Contains(modelID, "gpt-4o") {
		return "json"
	}
	return "verbose_json"
}

// ---- wire types ----

type transcriptionResponse struct {
	Text     string                     `json:"text"`
	Language string                     `json:"language,omitempty"`
	Duration float64                    `json:"duration,omitempty"`
	Segments []transcriptionSegmentWire `json:"segments,omitempty"`
}

type transcriptionSegmentWire struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// audioFormCall carries the per-endpoint knobs of the shared
// multipart-audio request flow behind the transcriptions and translations
// endpoints, which differ only in endpoint path, response_format policy,
// and an optional language field.
type audioFormCall struct {
	endpoint        string // final URL path segment: "transcriptions" or "translations"
	kind            string // for error messages: "transcription" or "translation"
	responseFormat  string
	mediaType       string
	audio           []byte
	language        string // written as a "language" field when non-empty
	prompt          string
	providerOptions map[string]any
	headers         map[string]string
}

// doAudioForm builds the multipart form, posts it, and returns the raw
// success response body (callers decode their own wire shape).
func doAudioForm(ctx context.Context, cfg Config, modelID string, r audioFormCall) ([]byte, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%s: base URL not configured", cfg.Name)
	}
	if err := multipartutil.ValidField("media type", r.mediaType); err != nil {
		return nil, fmt.Errorf("openaicompat: %w", err)
	}
	if err := multipartutil.ValidField("language", r.language); err != nil {
		return nil, fmt.Errorf("openaicompat: %w", err)
	}
	// r.prompt is intentionally not guarded: it's a free-text hint (may
	// legitimately contain quotes/newlines) that only ever reaches a
	// multipart field *value*, never a header — unlike
	// MediaType/Filename/field-name, its content cannot forge a header or
	// a new part.

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	ext := strings.TrimPrefix(transcribeutil.ExtForMediaType(r.mediaType), ".")
	if ext == "" {
		ext = "bin"
	}
	part, err := multipartutil.CreateFilePart(mw, "file", "audio."+ext, r.mediaType)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: create %s file part: %w", r.kind, err)
	}
	if _, err := part.Write(r.audio); err != nil {
		return nil, fmt.Errorf("openaicompat: write %s file part: %w", r.kind, err)
	}

	fields := [][2]string{{"model", modelID}}
	if r.language != "" {
		fields = append(fields, [2]string{"language", r.language})
	}
	if r.prompt != "" {
		fields = append(fields, [2]string{"prompt", r.prompt})
	}
	fields = append(fields, [2]string{"response_format", r.responseFormat})
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			return nil, fmt.Errorf("openaicompat: write %s field: %w", f[0], err)
		}
	}

	if err := multipartutil.ApplyProviderOptionsForm(mw, r.providerOptions, cfg.Name); err != nil {
		return nil, fmt.Errorf("openaicompat: apply provider options: %w", err)
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("openaicompat: close multipart writer: %w", err)
	}

	url := cfg.BaseURL + "/audio/" + r.endpoint
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: build %s request: %w", r.kind, err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	cfg.setAuthHeader(httpReq)
	httpheader.Apply(httpReq, r.headers, cfg.authHeaderName())

	resp, err := cfg.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openaicompat: read %s response: %w", r.kind, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}
	return body, nil
}

func (m *transcriptionModel) Transcribe(ctx context.Context, call provider.TranscriptionCall) (*provider.TranscriptionResponse, error) {
	body, err := doAudioForm(ctx, m.cfg, m.modelID, audioFormCall{
		endpoint:        "transcriptions",
		kind:            "transcription",
		responseFormat:  transcriptionResponseFormat(m.modelID),
		mediaType:       call.MediaType,
		audio:           call.Audio,
		language:        call.Language,
		prompt:          call.Prompt,
		providerOptions: call.ProviderOptions,
		headers:         call.Headers,
	})
	if err != nil {
		return nil, err
	}

	var wr transcriptionResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openaicompat: decode transcription response: %w", err)
	}

	segments := make([]provider.TranscriptSegment, len(wr.Segments))
	for i, s := range wr.Segments {
		segments[i] = provider.TranscriptSegment{
			Text:     s.Text,
			StartSec: s.Start,
			EndSec:   s.End,
		}
	}

	return &provider.TranscriptionResponse{
		Text:        wr.Text,
		Segments:    segments,
		Language:    wr.Language,
		DurationSec: wr.Duration,
		Raw:         json.RawMessage(body),
	}, nil
}
