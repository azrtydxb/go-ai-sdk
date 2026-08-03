package openai

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

type filesFixture struct {
	uploadAuth        string
	uploadFilename    string
	uploadPurpose     string
	uploadData        []byte
	uploadContentType string

	deleteMethod string
	deletePath   string
}

func newFilesFixture(t *testing.T, uploadStatus int, uploadBody string, deleteStatus int, deleteBody string) (*httptest.Server, *filesFixture) {
	t.Helper()
	f := &filesFixture{}

	mux := http.NewServeMux()
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		f.uploadAuth = r.Header.Get("Authorization")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				switch part.FormName() {
				case "file":
					f.uploadFilename = part.FileName()
					f.uploadContentType = part.Header.Get("Content-Type")
					f.uploadData, _ = io.ReadAll(part)
				case "purpose":
					b, _ := io.ReadAll(part)
					f.uploadPurpose = string(b)
				}
			}
		}
		w.WriteHeader(uploadStatus)
		w.Write([]byte(uploadBody))
	})
	mux.HandleFunc("/files/file-1", func(w http.ResponseWriter, r *http.Request) {
		f.deleteMethod = r.Method
		f.deletePath = r.URL.Path
		w.WriteHeader(deleteStatus)
		w.Write([]byte(deleteBody))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestFilesUploadFileHappyPath(t *testing.T) {
	srv, f := newFilesFixture(t, http.StatusOK, `{"id":"file-1","filename":"report.pdf","bytes":4}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	info, err := store.UploadFile(context.Background(), provider.FileUploadCall{
		Data:      []byte("data"),
		Filename:  "report.pdf",
		MediaType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.uploadAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want Bearer k", f.uploadAuth)
	}
	if f.uploadFilename != "report.pdf" {
		t.Errorf("uploaded filename = %q, want report.pdf", f.uploadFilename)
	}
	if string(f.uploadData) != "data" {
		t.Errorf("uploaded data = %q, want data", f.uploadData)
	}
	if f.uploadPurpose != "user_data" {
		t.Errorf("purpose = %q, want default user_data", f.uploadPurpose)
	}
	if info.ID != "file-1" || info.Filename != "report.pdf" || info.SizeBytes != 4 {
		t.Errorf("info = %+v, want {file-1 report.pdf ... 4 ...}", info)
	}
	if info.Raw == nil {
		t.Error("info.Raw = nil, want set")
	}
	if f.uploadContentType != "application/pdf" {
		t.Errorf("uploaded file part Content-Type = %q, want application/pdf (MediaType must not be dropped)", f.uploadContentType)
	}
}

// --- Fix wave IMPORTANT 2 — FileUploadCall.MediaType must not be silently
// dropped; an empty MediaType keeps the prior CreateFormFile behavior
// (application/octet-stream). ---

func TestFilesUploadFileEmptyMediaTypeDefaultsOctetStream(t *testing.T) {
	srv, f := newFilesFixture(t, http.StatusOK, `{"id":"file-1","filename":"a","bytes":1}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	if _, err := store.UploadFile(context.Background(), provider.FileUploadCall{
		Data:     []byte("a"),
		Filename: "a",
	}); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.uploadContentType != "application/octet-stream" {
		t.Errorf("uploaded file part Content-Type = %q, want application/octet-stream", f.uploadContentType)
	}
}

func TestFilesUploadFileCustomPurpose(t *testing.T) {
	srv, f := newFilesFixture(t, http.StatusOK, `{"id":"file-1","filename":"a","bytes":1}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	_, err := store.UploadFile(context.Background(), provider.FileUploadCall{
		Data:     []byte("d"),
		Filename: "a",
		Purpose:  "assistants",
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.uploadPurpose != "assistants" {
		t.Errorf("purpose = %q, want assistants", f.uploadPurpose)
	}
}

func TestFilesUploadFileErrorStatus(t *testing.T) {
	srv, _ := newFilesFixture(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key"}}`, http.StatusOK, "")
	store := New(WithAPIKey("bad"), WithBaseURL(srv.URL)).Files()

	_, err := store.UploadFile(context.Background(), provider.FileUploadCall{Data: []byte("d"), Filename: "a"})
	if err == nil {
		t.Fatal("UploadFile: want error for 401 response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *ai.APICallError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "invalid api key") {
		t.Errorf("Message = %q, want it to contain invalid api key", apiErr.Message)
	}
}

func TestFilesUploadFileRateLimited(t *testing.T) {
	srv, _ := newFilesFixture(t, http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	_, err := store.UploadFile(context.Background(), provider.FileUploadCall{Data: []byte("d"), Filename: "a"})
	if err == nil {
		t.Fatal("UploadFile: want error for 429 response")
	}
	var apiErr *ai.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *ai.APICallError", err)
	}
	if !apiErr.Retryable {
		t.Error("Retryable = false for 429, want true")
	}
}

func TestFilesUploadFileCtxCanceled(t *testing.T) {
	srv, _ := newFilesFixture(t, http.StatusOK, `{}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.UploadFile(ctx, provider.FileUploadCall{Data: []byte("d"), Filename: "a"})
	if err == nil {
		t.Fatal("UploadFile: want error for canceled context")
	}
}

func TestFilesDeleteFileHappyPath(t *testing.T) {
	srv, f := newFilesFixture(t, http.StatusOK, "", http.StatusOK, `{"id":"file-1","deleted":true}`)
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	err := store.DeleteFile(context.Background(), "file-1")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if f.deleteMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", f.deleteMethod)
	}
	if f.deletePath != "/files/file-1" {
		t.Errorf("path = %q, want /files/file-1", f.deletePath)
	}
}

func TestFilesDeleteFileErrorStatus(t *testing.T) {
	srv, _ := newFilesFixture(t, http.StatusOK, "", http.StatusNotFound, `{"error":{"message":"not found"}}`)
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	err := store.DeleteFile(context.Background(), "file-1")
	if err == nil {
		t.Fatal("DeleteFile: want error for 404 response")
	}
}

func TestFilesProviderName(t *testing.T) {
	store := New(WithAPIKey("k")).Files()
	if store.ProviderName() != "openai" {
		t.Errorf("ProviderName() = %q, want openai", store.ProviderName())
	}
}
