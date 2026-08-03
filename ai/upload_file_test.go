package ai

import (
	"errors"
	"testing"

	"github.com/azrtydxb/go-ai-sdk/ai/aitest"
	"github.com/azrtydxb/go-ai-sdk/provider"
)

func TestUploadFileHappyPath(t *testing.T) {
	m := &aitest.MockFileStore{UploadResponse: &provider.FileInfo{
		ID:        "file-1",
		Filename:  "report.pdf",
		SizeBytes: 4,
		MediaType: "application/pdf",
	}}
	res, err := UploadFile(t.Context(), UploadFileOpts{
		Store:     m,
		Data:      []byte("data"),
		Filename:  "report.pdf",
		MediaType: "application/pdf",
		Purpose:   "user_data",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.UploadCalls) != 1 {
		t.Fatalf("calls = %d, want 1", len(m.UploadCalls))
	}
	call := m.UploadCalls[0]
	if string(call.Data) != "data" || call.Filename != "report.pdf" || call.MediaType != "application/pdf" || call.Purpose != "user_data" {
		t.Fatalf("call mapped incorrectly: %+v", call)
	}
	if res.ID != "file-1" {
		t.Fatalf("res.ID = %q, want file-1", res.ID)
	}
}

func TestUploadFileNilStore(t *testing.T) {
	_, err := UploadFile(t.Context(), UploadFileOpts{Data: []byte("data"), Filename: "f.pdf"})
	if !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("err = %v, want ErrStoreRequired", err)
	}
}

func TestUploadFileEmptyData(t *testing.T) {
	m := &aitest.MockFileStore{}
	_, err := UploadFile(t.Context(), UploadFileOpts{Store: m, Filename: "f.pdf"})
	if !errors.Is(err, ErrDataRequired) {
		t.Fatalf("err = %v, want ErrDataRequired", err)
	}
}

func TestUploadFileEmptyFilename(t *testing.T) {
	m := &aitest.MockFileStore{}
	_, err := UploadFile(t.Context(), UploadFileOpts{Store: m, Data: []byte("data")})
	if !errors.Is(err, ErrFilenameRequired) {
		t.Fatalf("err = %v, want ErrFilenameRequired", err)
	}
}

func TestUploadFileRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockFileStore{UploadErr: NewAPICallError(500, "https://x", "", "boom")}
	_, err := UploadFile(t.Context(), UploadFileOpts{Store: m, Data: []byte("data"), Filename: "f.pdf"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.UploadCalls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.UploadCalls))
	}
}

func TestUploadFileNonRetryableError(t *testing.T) {
	m := &aitest.MockFileStore{UploadErr: NewAPICallError(400, "https://x", "", "bad request")}
	_, err := UploadFile(t.Context(), UploadFileOpts{Store: m, Data: []byte("data"), Filename: "f.pdf"})
	if err == nil {
		t.Fatal("want error")
	}
	var re *RetryError
	if errors.As(err, &re) {
		t.Fatalf("err = %v; want it not wrapped in RetryError for a non-retryable failure", err)
	}
	if len(m.UploadCalls) != 1 {
		t.Fatalf("calls = %d, want 1 (no retries for non-retryable error)", len(m.UploadCalls))
	}
}

func TestDeleteFileHappyPath(t *testing.T) {
	m := &aitest.MockFileStore{}
	err := DeleteFile(t.Context(), DeleteFileOpts{Store: m, ID: "file-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.DeleteCalls) != 1 || m.DeleteCalls[0] != "file-1" {
		t.Fatalf("DeleteCalls = %v, want [file-1]", m.DeleteCalls)
	}
}

func TestDeleteFileNilStore(t *testing.T) {
	err := DeleteFile(t.Context(), DeleteFileOpts{ID: "file-1"})
	if !errors.Is(err, ErrStoreRequired) {
		t.Fatalf("err = %v, want ErrStoreRequired", err)
	}
}

func TestDeleteFileEmptyID(t *testing.T) {
	m := &aitest.MockFileStore{}
	err := DeleteFile(t.Context(), DeleteFileOpts{Store: m})
	if !errors.Is(err, ErrIDRequired) {
		t.Fatalf("err = %v, want ErrIDRequired", err)
	}
}

func TestDeleteFileRetriesOnRetryableError(t *testing.T) {
	m := &aitest.MockFileStore{DeleteErr: NewAPICallError(500, "https://x", "", "boom")}
	err := DeleteFile(t.Context(), DeleteFileOpts{Store: m, ID: "file-1"})
	var re *RetryError
	if !errors.As(err, &re) || re.Attempts != 3 {
		t.Fatalf("err = %v; want RetryError{Attempts:3}", err)
	}
	if len(m.DeleteCalls) != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", len(m.DeleteCalls))
	}
}
