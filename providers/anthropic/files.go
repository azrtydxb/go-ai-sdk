package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/azrtydxb/go-ai-sdk/internal/multipartutil"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// filesBetaHeader is the anthropic-beta header value required by the Files
// API. It is set only on files.go's own requests — never on the shared
// language-model path (providers/anthropic/language_model.go).
const filesBetaHeader = "files-api-2025-04-14"

// Files returns a provider.FileStore backed by Anthropic's Files API
// (https://api.anthropic.com/v1/files), for uploading files to reference
// from a later prompt via provider.FilePart.FileID.
func (p *Provider) Files() provider.FileStore {
	return &fileStore{provider: p}
}

type fileStore struct {
	provider *Provider
}

func (s *fileStore) ProviderName() string { return "anthropic" }

type fileWireResponse struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
}

// createFilePart adds the "file" part to mw, using filename and, when
// mediaType is non-empty, a Content-Type header carrying it — mirroring
// internal/openaicompat's translation upload path. An empty mediaType
// falls back to mw.CreateFormFile, which (per net/http's sniffing
// convention) always writes "application/octet-stream" and leaves
// call.FileUploadCall.MediaType's information dropped, matching prior
// behavior for callers that don't set MediaType.
func createFilePart(mw *multipart.Writer, filename, mediaType string) (io.Writer, error) {
	if err := multipartutil.ValidField("filename", filename); err != nil {
		return nil, err
	}
	if err := multipartutil.ValidField("media type", mediaType); err != nil {
		return nil, err
	}
	if mediaType == "" {
		return mw.CreateFormFile("file", filename)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", mediaType)
	return mw.CreatePart(h)
}

// UploadFile implements provider.FileStore. It POSTs a multipart request to
// {base}/v1/files with a "file" field, sending the x-api-key,
// anthropic-version, and anthropic-beta: files-api-2025-04-14 headers.
func (s *fileStore) UploadFile(ctx context.Context, call provider.FileUploadCall) (*provider.FileInfo, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := call.Filename
	if filename == "" {
		filename = "file"
	}
	fw, err := createFilePart(mw, filename, call.MediaType)
	if err != nil {
		return nil, fmt.Errorf("anthropic: create file part: %w", err)
	}
	if _, err := fw.Write(call.Data); err != nil {
		return nil, fmt.Errorf("anthropic: write file part: %w", err)
	}

	if opts, ok := call.ProviderOptions["anthropic"].(map[string]any); ok {
		for k, v := range opts {
			if err := multipartutil.ValidField("provider option field name", k); err != nil {
				return nil, fmt.Errorf("anthropic: %w", err)
			}
			sv := fmt.Sprint(v)
			if err := multipartutil.ValidField("provider option field value", sv); err != nil {
				return nil, fmt.Errorf("anthropic: %w", err)
			}
			if err := mw.WriteField(k, sv); err != nil {
				return nil, fmt.Errorf("anthropic: write provider option field %q: %w", k, err)
			}
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("anthropic: close multipart writer: %w", err)
	}

	url := s.provider.baseURL + "/v1/files"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set(anthropicAuthHeader, s.provider.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-beta", filesBetaHeader)

	resp, err := s.provider.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	var wr fileWireResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("anthropic: decode upload response: %w", err)
	}

	return &provider.FileInfo{
		ID:        wr.ID,
		Filename:  wr.Filename,
		SizeBytes: wr.SizeBytes,
		MediaType: wr.MimeType,
		Raw:       json.RawMessage(body),
	}, nil
}

// DeleteFile implements provider.FileStore. It DELETEs {base}/v1/files/{id}
// with the same anthropic-beta header as UploadFile.
func (s *fileStore) DeleteFile(ctx context.Context, id string) error {
	url := s.provider.baseURL + "/v1/files/" + id
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("anthropic: build delete request: %w", err)
	}
	httpReq.Header.Set(anthropicAuthHeader, s.provider.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-beta", filesBetaHeader)

	resp, err := s.provider.client().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("anthropic: read delete response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp, body)
	}

	return nil
}
