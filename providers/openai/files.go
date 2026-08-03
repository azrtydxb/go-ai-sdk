package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

// defaultFilePurpose is sent when FileUploadCall.Purpose is empty.
const defaultFilePurpose = "user_data"

// Files returns a provider.FileStore backed by OpenAI's Files API
// (https://api.openai.com/v1/files), for uploading files to reference from
// a later prompt via provider.FilePart.FileID.
func (p *Provider) Files() provider.FileStore {
	return &fileStore{provider: p}
}

type fileStore struct {
	provider *Provider
}

func (s *fileStore) ProviderName() string { return "openai" }

func (s *fileStore) client() *http.Client {
	if s.provider.httpClient != nil {
		return s.provider.httpClient
	}
	return http.DefaultClient
}

type fileWireResponse struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Bytes    int64  `json:"bytes"`
}

type fileWireError struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func fileErrorMessage(body []byte) string {
	var we fileWireError
	if err := json.Unmarshal(body, &we); err == nil && we.Error.Message != "" {
		return we.Error.Message
	}
	return string(body)
}

func fileAPIError(resp *http.Response, body []byte) error {
	return ai.NewAPICallError(resp.StatusCode, resp.Request.URL.String(), string(body), fileErrorMessage(body))
}

// createFilePart adds the "file" part to mw, using filename and, when
// mediaType is non-empty, a Content-Type header carrying it — mirroring
// internal/openaicompat's translation upload path. An empty mediaType
// falls back to mw.CreateFormFile, which (per net/http's sniffing
// convention) always writes "application/octet-stream" and leaves
// call.FileUploadCall.MediaType's information dropped, matching prior
// behavior for callers that don't set MediaType.
func createFilePart(mw *multipart.Writer, filename, mediaType string) (io.Writer, error) {
	if mediaType == "" {
		return mw.CreateFormFile("file", filename)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", mediaType)
	return mw.CreatePart(h)
}

// UploadFile implements provider.FileStore. It POSTs a multipart request to
// {base}/files with fields "file" and "purpose" (defaulting to "user_data").
func (s *fileStore) UploadFile(ctx context.Context, call provider.FileUploadCall) (*provider.FileInfo, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	filename := call.Filename
	if filename == "" {
		filename = "file"
	}
	fw, err := createFilePart(mw, filename, call.MediaType)
	if err != nil {
		return nil, fmt.Errorf("openai: create file part: %w", err)
	}
	if _, err := fw.Write(call.Data); err != nil {
		return nil, fmt.Errorf("openai: write file part: %w", err)
	}

	purpose := call.Purpose
	if purpose == "" {
		purpose = defaultFilePurpose
	}
	if err := mw.WriteField("purpose", purpose); err != nil {
		return nil, fmt.Errorf("openai: write purpose field: %w", err)
	}

	if opts, ok := call.ProviderOptions["openai"].(map[string]any); ok {
		for k, v := range opts {
			if err := mw.WriteField(k, fmt.Sprint(v)); err != nil {
				return nil, fmt.Errorf("openai: write provider option field %q: %w", k, err)
			}
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("openai: close multipart writer: %w", err)
	}

	url := s.provider.baseURL + "/files"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("openai: build upload request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())
	httpReq.Header.Set("Authorization", "Bearer "+s.provider.apiKey)

	resp, err := s.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read upload response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fileAPIError(resp, body)
	}

	var wr fileWireResponse
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("openai: decode upload response: %w", err)
	}

	return &provider.FileInfo{
		ID:        wr.ID,
		Filename:  wr.Filename,
		SizeBytes: wr.Bytes,
		Raw:       json.RawMessage(body),
	}, nil
}

// DeleteFile implements provider.FileStore. It DELETEs {base}/files/{id}.
func (s *fileStore) DeleteFile(ctx context.Context, id string) error {
	url := s.provider.baseURL + "/files/" + id
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("openai: build delete request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.provider.apiKey)

	resp, err := s.client().Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("openai: read delete response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fileAPIError(resp, body)
	}

	return nil
}
