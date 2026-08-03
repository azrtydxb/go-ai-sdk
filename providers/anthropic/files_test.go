package anthropic

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

type anthFilesFixture struct {
	uploadAPIKey      string
	uploadVersion     string
	uploadBeta        string
	uploadFile        []byte
	uploadName        string
	uploadContentType string

	deleteMethod string
	deletePath   string
	deleteBeta   string
}

func newAnthFilesFixture(t *testing.T, uploadStatus int, uploadBody string, deleteStatus int, deleteBody string) (*httptest.Server, *anthFilesFixture) {
	t.Helper()
	f := &anthFilesFixture{}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		f.uploadAPIKey = r.Header.Get("x-api-key")
		f.uploadVersion = r.Header.Get("anthropic-version")
		f.uploadBeta = r.Header.Get("anthropic-beta")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err == nil {
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				if part.FormName() == "file" {
					f.uploadName = part.FileName()
					f.uploadContentType = part.Header.Get("Content-Type")
					f.uploadFile, _ = io.ReadAll(part)
				}
			}
		}
		w.WriteHeader(uploadStatus)
		w.Write([]byte(uploadBody))
	})
	mux.HandleFunc("/v1/files/file-1", func(w http.ResponseWriter, r *http.Request) {
		f.deleteMethod = r.Method
		f.deletePath = r.URL.Path
		f.deleteBeta = r.Header.Get("anthropic-beta")
		w.WriteHeader(deleteStatus)
		w.Write([]byte(deleteBody))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

func TestFilesUploadFileHappyPath(t *testing.T) {
	srv, f := newAnthFilesFixture(t, http.StatusOK, `{"id":"file_1","filename":"report.pdf","size_bytes":4,"mime_type":"application/pdf"}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	info, err := store.UploadFile(context.Background(), provider.FileUploadCall{
		Data:      []byte("data"),
		Filename:  "report.pdf",
		MediaType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.uploadAPIKey != "k" {
		t.Errorf("x-api-key = %q, want k", f.uploadAPIKey)
	}
	if f.uploadVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", f.uploadVersion, anthropicVersion)
	}
	if f.uploadBeta != filesBetaHeader {
		t.Errorf("anthropic-beta = %q, want %q", f.uploadBeta, filesBetaHeader)
	}
	if f.uploadName != "report.pdf" {
		t.Errorf("uploaded filename = %q, want report.pdf", f.uploadName)
	}
	if string(f.uploadFile) != "data" {
		t.Errorf("uploaded data = %q, want data", f.uploadFile)
	}
	if info.ID != "file_1" || info.Filename != "report.pdf" || info.SizeBytes != 4 || info.MediaType != "application/pdf" {
		t.Errorf("info = %+v, want {file_1 report.pdf 4 application/pdf ...}", info)
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
	srv, f := newAnthFilesFixture(t, http.StatusOK, `{"id":"file_1","filename":"a","size_bytes":1}`, http.StatusOK, "")
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

func TestFilesUploadFileErrorStatus(t *testing.T) {
	srv, _ := newAnthFilesFixture(t, http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"invalid api key"}}`, http.StatusOK, "")
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
	srv, _ := newAnthFilesFixture(t, http.StatusTooManyRequests, `{"error":{"type":"rate_limit_error","message":"rate limited"}}`, http.StatusOK, "")
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
	srv, _ := newAnthFilesFixture(t, http.StatusOK, `{}`, http.StatusOK, "")
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.UploadFile(ctx, provider.FileUploadCall{Data: []byte("d"), Filename: "a"})
	if err == nil {
		t.Fatal("UploadFile: want error for canceled context")
	}
}

func TestFilesDeleteFileHappyPath(t *testing.T) {
	srv, f := newAnthFilesFixture(t, http.StatusOK, "", http.StatusOK, `{"id":"file_1","type":"file_deleted"}`)
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	err := store.DeleteFile(context.Background(), "file-1")
	if err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if f.deleteMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", f.deleteMethod)
	}
	if f.deletePath != "/v1/files/file-1" {
		t.Errorf("path = %q, want /v1/files/file-1", f.deletePath)
	}
	if f.deleteBeta != filesBetaHeader {
		t.Errorf("anthropic-beta = %q, want %q", f.deleteBeta, filesBetaHeader)
	}
}

func TestFilesDeleteFileErrorStatus(t *testing.T) {
	srv, _ := newAnthFilesFixture(t, http.StatusOK, "", http.StatusNotFound, `{"error":{"type":"not_found_error","message":"not found"}}`)
	store := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Files()

	err := store.DeleteFile(context.Background(), "file-1")
	if err == nil {
		t.Fatal("DeleteFile: want error for 404 response")
	}
}

func TestFilesProviderName(t *testing.T) {
	store := New(WithAPIKey("k")).Files()
	if store.ProviderName() != "anthropic" {
		t.Errorf("ProviderName() = %q, want anthropic", store.ProviderName())
	}
}

// TestFilesBetaHeaderNotOnLanguageModelPath verifies the anthropic-beta
// header used by the Files API is never sent on the shared
// language-model path.
func TestFilesBetaHeaderNotOnLanguageModelPath(t *testing.T) {
	var gotBeta string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"model":"claude-test","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	model := New(WithAPIKey("k"), WithBaseURL(srv.URL)).Model("claude-test")
	_, err := model.Generate(context.Background(), provider.Call{
		Messages: []provider.Message{provider.UserText("simple")},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotBeta != "" {
		t.Errorf("anthropic-beta on language-model request = %q, want empty", gotBeta)
	}
}
