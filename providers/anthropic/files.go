package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/azrtydxb/go-ai-sdk/internal/httpheader"
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

// postMultipart POSTs a multipart request built by build to {base}{path},
// sending the x-api-key, anthropic-version, and given anthropic-beta
// headers (plus any extra caller headers), then decodes the 2xx response
// into out. Returns the raw response body for Raw fields. Shared by
// UploadFile and UploadSkill.
func (p *Provider) postMultipart(ctx context.Context, path, betaHeader string, headers map[string]string, build func(*multipart.Writer) error, out any) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if err := build(mw); err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("anthropic: close multipart writer: %w", err)
	}

	url := p.baseURL + path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set(anthropicAuthHeader, p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("anthropic-beta", betaHeader)
	httpheader.Apply(httpReq, headers, anthropicAuthHeader)

	resp, err := p.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp, body)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return nil, fmt.Errorf("anthropic: decode upload response: %w", err)
	}
	return body, nil
}

// UploadFile implements provider.FileStore. It POSTs a multipart request to
// {base}/v1/files with a "file" field, sending the x-api-key,
// anthropic-version, and anthropic-beta: files-api-2025-04-14 headers.
func (s *fileStore) UploadFile(ctx context.Context, call provider.FileUploadCall) (*provider.FileInfo, error) {
	filename := call.Filename
	if filename == "" {
		filename = "file"
	}

	var wr fileWireResponse
	body, err := s.provider.postMultipart(ctx, "/v1/files", filesBetaHeader, call.Headers, func(mw *multipart.Writer) error {
		fw, err := multipartutil.CreateFilePart(mw, "file", filename, call.MediaType)
		if err != nil {
			return fmt.Errorf("create file part: %w", err)
		}
		if _, err := fw.Write(call.Data); err != nil {
			return fmt.Errorf("write file part: %w", err)
		}
		return multipartutil.ApplyProviderOptionsForm(mw, call.ProviderOptions, "anthropic")
	}, &wr)
	if err != nil {
		return nil, err
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
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("anthropic: read delete response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp, body)
	}

	return nil
}
